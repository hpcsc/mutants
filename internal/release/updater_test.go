//go:build unit

package release_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hpcsc/mutants/internal/release"
	"github.com/stretchr/testify/require"
)

type fakeRelease struct {
	tag        string
	prerelease bool
	draft      bool
	published  time.Time
	assets     map[string][]byte
}

// fakeGitHub serves its releases in the order of the slice.
type fakeGitHub struct {
	releases []fakeRelease
}

func (f fakeGitHub) serve(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	describe := func(rel fakeRelease) map[string]any {
		var assets []map[string]string
		for name := range rel.assets {
			assets = append(assets, map[string]string{"name": name, "url": server.URL + "/assets/" + rel.tag + "/" + name})
		}
		return map[string]any{"tag_name": rel.tag, "prerelease": rel.prerelease, "draft": rel.draft, "published_at": rel.published, "assets": assets}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/hpcsc/mutants/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		for _, rel := range f.releases {
			if !rel.prerelease && !rel.draft {
				require.NoError(t, json.NewEncoder(w).Encode(describe(rel)))
				return
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/repos/hpcsc/mutants/releases", func(w http.ResponseWriter, r *http.Request) {
		all := []map[string]any{}
		for _, rel := range f.releases {
			all = append(all, describe(rel))
		}
		require.NoError(t, json.NewEncoder(w).Encode(all))
	})
	mux.HandleFunc("/assets/{tag}/{name}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "assets need Accept: application/octet-stream", http.StatusBadRequest)
			return
		}
		for _, rel := range f.releases {
			if rel.tag == r.PathValue("tag") {
				data := rel.assets[r.PathValue("name")]
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				_, _ = w.Write(data)
				return
			}
		}
		http.NotFound(w, r)
	})
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func archiveHolding(t *testing.T, binary string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	files := tar.NewWriter(gz)
	for name, content := range map[string]string{"README.md": "# mutants\n", "mutants": binary} {
		require.NoError(t, files.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}))
		_, err := files.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, files.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func checksumsFor(files map[string][]byte) []byte {
	var buf bytes.Buffer
	for name, data := range files {
		sum := sha256.Sum256(data)
		fmt.Fprintf(&buf, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return buf.Bytes()
}

func TestUpdater(t *testing.T) {
	ctx := context.Background()
	releaseWith := func(t *testing.T, tag string, archives map[string][]byte) *release.Client {
		t.Helper()
		assets := map[string][]byte{"checksums.txt": checksumsFor(archives)}
		for name, data := range archives {
			assets[name] = data
		}
		server := fakeGitHub{releases: []fakeRelease{
			{tag: tag, assets: assets},
		}}.serve(t)
		return release.NewClient(server.Client(), server.URL, "hpcsc/mutants", "")
	}
	clientFor := func(t *testing.T, releases ...fakeRelease) *release.Client {
		t.Helper()
		server := fakeGitHub{releases: releases}.serve(t)
		return release.NewClient(server.Client(), server.URL, "hpcsc/mutants", "")
	}
	day := func(n int) time.Time {
		return time.Date(2026, 9, n, 12, 0, 0, 0, time.UTC)
	}
	installedBinary := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "mutants")
		require.NoError(t, os.WriteFile(path, []byte("old binary"), 0o755))
		return path
	}

	t.Run("check", func(t *testing.T) {
		t.Run("the release channel takes the latest release, not a newer prerelease", func(t *testing.T) {
			client := clientFor(t,
				fakeRelease{tag: "v0.2.1-3.gccccccc", prerelease: true, published: day(3)},
				fakeRelease{tag: "v0.2.0", published: day(2)},
			)

			check, err := release.NewUpdater(client, "v0.1.0", "darwin-arm64", "").Check(ctx, release.Releases)

			require.NoError(t, err)
			require.Equal(t, "v0.2.0", check.Latest.Tag)
			require.False(t, check.UpToDate)
		})

		t.Run("the prerelease channel takes the prerelease that was published last", func(t *testing.T) {
			client := clientFor(t,
				fakeRelease{tag: "v0.2.0", published: day(4)},
				fakeRelease{tag: "v0.2.1-2.gbbbbbbb", prerelease: true, published: day(2)},
				fakeRelease{tag: "v0.2.1-3.gccccccc", prerelease: true, published: day(3)},
			)

			check, err := release.NewUpdater(client, "v0.2.0", "darwin-arm64", "").Check(ctx, release.Prereleases)

			require.NoError(t, err)
			require.Equal(t, "v0.2.1-3.gccccccc", check.Latest.Tag)
		})

		t.Run("the prerelease channel skips a draft", func(t *testing.T) {
			client := clientFor(t,
				fakeRelease{tag: "v0.2.1-4.gddddddd", prerelease: true, draft: true, published: day(4)},
				fakeRelease{tag: "v0.2.1-3.gccccccc", prerelease: true, published: day(3)},
			)

			check, err := release.NewUpdater(client, "v0.2.0", "darwin-arm64", "").Check(ctx, release.Prereleases)

			require.NoError(t, err)
			require.Equal(t, "v0.2.1-3.gccccccc", check.Latest.Tag)
		})

		t.Run("the latest build of a channel is up to date", func(t *testing.T) {
			client := clientFor(t, fakeRelease{tag: "v0.2.0", published: day(2)})

			check, err := release.NewUpdater(client, "v0.2.0", "darwin-arm64", "").Check(ctx, release.Releases)

			require.NoError(t, err)
			require.True(t, check.UpToDate)
		})

		t.Run("a prerelease build is not up to date on the release channel, so an update goes back to the latest release", func(t *testing.T) {
			client := clientFor(t,
				fakeRelease{tag: "v0.2.1-3.gccccccc", prerelease: true, published: day(3)},
				fakeRelease{tag: "v0.2.0", published: day(2)},
			)

			check, err := release.NewUpdater(client, "v0.2.1-3.gccccccc", "darwin-arm64", "").Check(ctx, release.Releases)

			require.NoError(t, err)
			require.Equal(t, "v0.2.0", check.Latest.Tag)
			require.False(t, check.UpToDate)
		})

		t.Run("a repository with no release says so", func(t *testing.T) {
			client := clientFor(t)

			_, err := release.NewUpdater(client, "v0.1.0", "darwin-arm64", "").Check(ctx, release.Releases)

			require.ErrorIs(t, err, release.ErrNoRelease)
		})

		t.Run("a repository with only releases has no prerelease", func(t *testing.T) {
			client := clientFor(t, fakeRelease{tag: "v0.2.0", published: day(2)})

			_, err := release.NewUpdater(client, "v0.2.0", "darwin-arm64", "").Check(ctx, release.Prereleases)

			require.ErrorIs(t, err, release.ErrNoRelease)
		})
	})

	t.Run("install", func(t *testing.T) {
		t.Run("replaces the executable with the binary from the archive for this platform", func(t *testing.T) {
			client := releaseWith(t, "v0.2.0", map[string][]byte{
				"mutants-darwin-arm64.tar.gz": archiveHolding(t, "darwin arm64 binary"),
				"mutants-linux-amd64.tar.gz":  archiveHolding(t, "linux amd64 binary"),
			})
			path := installedBinary(t)
			updater := release.NewUpdater(client, "v0.1.0", "darwin-arm64", path)
			check, err := updater.Check(ctx, release.Releases)
			require.NoError(t, err)

			require.NoError(t, updater.Install(ctx, check.Latest, nil))

			installed, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "darwin arm64 binary", string(installed))
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
		})

		t.Run("reports the bytes of the archive as they arrive, up to the size of the archive", func(t *testing.T) {
			archive := archiveHolding(t, "darwin arm64 binary")
			client := releaseWith(t, "v0.2.0", map[string][]byte{"mutants-darwin-arm64.tar.gz": archive})
			updater := release.NewUpdater(client, "v0.1.0", "darwin-arm64", installedBinary(t))
			check, err := updater.Check(ctx, release.Releases)
			require.NoError(t, err)
			var reported [][2]int64

			err = updater.Install(ctx, check.Latest, func(done, total int64) {
				reported = append(reported, [2]int64{done, total})
			})

			require.NoError(t, err)
			require.NotEmpty(t, reported)
			size := int64(len(archive))
			require.Equal(t, [2]int64{size, size}, reported[len(reported)-1])
		})

		t.Run("leaves the executable alone when the download does not match its checksum", func(t *testing.T) {
			archive := archiveHolding(t, "new binary")
			assets := map[string][]byte{
				"mutants-darwin-arm64.tar.gz": archive,
				"checksums.txt":               checksumsFor(map[string][]byte{"mutants-darwin-arm64.tar.gz": []byte("something else")}),
			}
			server := fakeGitHub{releases: []fakeRelease{
				{tag: "v0.2.0", assets: assets},
			}}.serve(t)
			path := installedBinary(t)
			updater := release.NewUpdater(release.NewClient(server.Client(), server.URL, "hpcsc/mutants", ""), "v0.1.0", "darwin-arm64", path)
			check, err := updater.Check(ctx, release.Releases)
			require.NoError(t, err)

			err = updater.Install(ctx, check.Latest, nil)

			require.ErrorContains(t, err, "does not match its checksum")
			installed, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			require.Equal(t, "old binary", string(installed))
		})

		t.Run("a release with no archive for this platform names the platform", func(t *testing.T) {
			client := releaseWith(t, "v0.2.0", map[string][]byte{"mutants-linux-amd64.tar.gz": archiveHolding(t, "linux amd64 binary")})
			path := installedBinary(t)
			updater := release.NewUpdater(client, "v0.1.0", "darwin-arm64", path)
			check, err := updater.Check(ctx, release.Releases)
			require.NoError(t, err)

			err = updater.Install(ctx, check.Latest, nil)

			require.ErrorContains(t, err, "darwin-arm64")
		})
	})
}
