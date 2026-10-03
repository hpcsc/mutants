//go:build unit

package golang_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/stretchr/testify/require"
)

type cacheRequest struct {
	ID       int64
	Command  string
	ActionID []byte `json:",omitempty"`
	OutputID []byte `json:",omitempty"`
	BodySize int64  `json:",omitempty"`
}

type cacheResponse struct {
	ID            int64
	Err           string
	KnownCommands []string
	Miss          bool
	OutputID      []byte
	Size          int64
	DiskPath      string
}

type cacheSession struct {
	t         *testing.T
	requests  io.Writer
	responses *json.Decoder
	served    chan error
	lastID    int64
}

func startBuildCache(t *testing.T, userCache, mutantCache string) *cacheSession {
	t.Helper()
	requestReader, requestWriter := io.Pipe()
	responseReader, responseWriter := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- golang.ServeBuildCache(requestReader, responseWriter, userCache, mutantCache)
		responseWriter.Close()
	}()
	t.Cleanup(func() { requestWriter.Close() })
	session := &cacheSession{t: t, requests: requestWriter, responses: json.NewDecoder(responseReader), served: served}
	var first cacheResponse
	require.NoError(t, session.responses.Decode(&first))
	require.Equal(t, []string{"get", "put", "close"}, first.KnownCommands)
	return session
}

// send writes a body as go does: a JSON string of base64 on the line after the request.
func (s *cacheSession) send(request cacheRequest, body []byte) cacheResponse {
	s.t.Helper()
	s.lastID++
	request.ID = s.lastID
	request.BodySize = int64(len(body))
	lines, err := json.Marshal(request)
	require.NoError(s.t, err)
	lines = append(lines, '\n')
	if len(body) > 0 {
		encoded, err := json.Marshal(body)
		require.NoError(s.t, err)
		lines = append(append(lines, encoded...), '\n')
	}
	_, err = s.requests.Write(lines)
	require.NoError(s.t, err)
	var response cacheResponse
	require.NoError(s.t, s.responses.Decode(&response))
	require.Equal(s.t, request.ID, response.ID)
	return response
}

func id(text string) []byte {
	sum := sha256.Sum256([]byte(text))
	return sum[:]
}

func cachePath(folder string, id []byte, suffix string) string {
	name := hex.EncodeToString(id)
	return filepath.Join(folder, name[:2], name+suffix)
}

func writeCacheFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// writeEntry writes an entry in the format of the build cache of go, as cmd/go/internal/cache does.
func writeEntry(t *testing.T, folder string, action, output []byte, body string) {
	t.Helper()
	writeCacheFile(t, cachePath(folder, output, "-d"), body)
	writeCacheFile(t, cachePath(folder, action, "-a"), fmt.Sprintf("v1 %x %x %20d %20d\n", action, output, len(body), time.Now().UnixNano()))
}

func filesIn(t *testing.T, folder string) []string {
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	}))
	return files
}

func entriesIn(t *testing.T, folder string) []string {
	t.Helper()
	return slices.DeleteFunc(filesIn(t, folder), func(path string) bool { return !strings.HasSuffix(path, "-a") })
}

func goBuild(t *testing.T, folder, userCache string, program ...string) {
	t.Helper()
	quoted := make([]string, len(program))
	for i, argument := range program {
		quoted[i] = "'" + argument + "'"
	}
	build := exec.Command("go", "build", ".")
	build.Dir = folder
	build.Env = append(os.Environ(), "GOFLAGS=", "GOCACHE="+userCache, "GOCACHEPROG="+strings.Join(quoted, " "), buildCacheLog+"="+filepath.Join(t.TempDir(), "log"))
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestBuildCache(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		t.Run("gives the output of an entry in the user cache", func(t *testing.T) {
			userCache := t.TempDir()
			writeEntry(t, userCache, id("action"), id("output"), "compiled")
			cache := startBuildCache(t, userCache, t.TempDir())

			response := cache.send(cacheRequest{Command: "get", ActionID: id("action")}, nil)

			require.Equal(t, cacheResponse{ID: response.ID, OutputID: id("output"), Size: 8, DiskPath: cachePath(userCache, id("output"), "-d")}, response)
		})

		t.Run("an action that no cache holds is a miss", func(t *testing.T) {
			userCache := t.TempDir()
			writeEntry(t, userCache, id("other action"), id("output"), "compiled")
			cache := startBuildCache(t, userCache, t.TempDir())

			response := cache.send(cacheRequest{Command: "get", ActionID: id("action")}, nil)

			require.True(t, response.Miss)
		})

		t.Run("an entry in another format is a miss", func(t *testing.T) {
			userCache := t.TempDir()
			writeCacheFile(t, cachePath(userCache, id("output"), "-d"), "compiled")
			writeCacheFile(t, cachePath(userCache, id("action"), "-a"), fmt.Sprintf("v2 %x %x %20d %20d\n", id("action"), id("output"), 8, time.Now().UnixNano()))
			cache := startBuildCache(t, userCache, t.TempDir())

			response := cache.send(cacheRequest{Command: "get", ActionID: id("action")}, nil)

			require.True(t, response.Miss)
		})

		t.Run("an output whose size differs from its entry is a miss", func(t *testing.T) {
			userCache := t.TempDir()
			writeEntry(t, userCache, id("action"), id("output"), "compiled")
			writeCacheFile(t, cachePath(userCache, id("output"), "-d"), "cut")
			cache := startBuildCache(t, userCache, t.TempDir())

			response := cache.send(cacheRequest{Command: "get", ActionID: id("action")}, nil)

			require.True(t, response.Miss)
		})
	})

	t.Run("put", func(t *testing.T) {
		t.Run("writes the output to the mutant cache, where a later get finds it, and leaves the user cache as it was", func(t *testing.T) {
			userCache, mutantCache := t.TempDir(), t.TempDir()
			writeEntry(t, userCache, id("other action"), id("other output"), "compiled")
			before := filesIn(t, userCache)
			cache := startBuildCache(t, userCache, mutantCache)

			put := cache.send(cacheRequest{Command: "put", ActionID: id("action"), OutputID: id("output")}, []byte("mutated"))
			got := cache.send(cacheRequest{Command: "get", ActionID: id("action")}, nil)

			require.Empty(t, put.Err)
			require.Equal(t, cachePath(mutantCache, id("output"), "-d"), put.DiskPath)
			written, err := os.ReadFile(put.DiskPath)
			require.NoError(t, err)
			require.Equal(t, "mutated", string(written))
			require.Equal(t, cacheResponse{ID: got.ID, OutputID: id("output"), Size: 7, DiskPath: put.DiskPath}, got)
			require.Equal(t, before, filesIn(t, userCache))
		})

		t.Run("writes an empty output for a put with no body", func(t *testing.T) {
			mutantCache := t.TempDir()
			cache := startBuildCache(t, t.TempDir(), mutantCache)

			put := cache.send(cacheRequest{Command: "put", ActionID: id("action"), OutputID: id("output")}, nil)
			got := cache.send(cacheRequest{Command: "get", ActionID: id("action")}, nil)

			require.Empty(t, put.Err)
			require.Equal(t, cacheResponse{ID: got.ID, OutputID: id("output"), DiskPath: cachePath(mutantCache, id("output"), "-d")}, got)
		})
	})

	t.Run("close", func(t *testing.T) {
		t.Run("answers, and stops", func(t *testing.T) {
			cache := startBuildCache(t, t.TempDir(), t.TempDir())

			response := cache.send(cacheRequest{Command: "close"}, nil)

			require.Empty(t, response.Err)
			require.NoError(t, <-cache.served)
		})
	})

	t.Run("a build of go", func(t *testing.T) {
		t.Run("reads the entries that go wrote to the user cache, and writes each new entry to the mutant cache", func(t *testing.T) {
			executable, err := os.Executable()
			require.NoError(t, err)
			root := newModule(t, map[string]string{"one/one.go": "package one\n\nfunc One() int { return 1 }\n"})
			// go keeps the index of a folder in its cache only when the files of the folder are 2 seconds old
			hourAgo := time.Now().Add(-time.Hour)
			for _, path := range []string{"one/one.go", "one"} {
				require.NoError(t, os.Chtimes(filepath.Join(root, path), hourAgo, hourAgo))
			}
			userCache := t.TempDir()
			goBuild(t, filepath.Join(root, "one"), userCache)
			before := filesIn(t, userCache)
			unchanged, changed := t.TempDir(), t.TempDir()

			goBuild(t, filepath.Join(root, "one"), userCache, executable, userCache, unchanged)
			require.NoError(t, os.WriteFile(filepath.Join(root, "one", "one.go"), []byte("package one\n\nfunc One() int { return 2 }\n"), 0o644))
			goBuild(t, filepath.Join(root, "one"), userCache, executable, userCache, changed)

			require.Empty(t, entriesIn(t, unchanged))
			require.NotEmpty(t, entriesIn(t, changed))
			require.Equal(t, before, filesIn(t, userCache))
		})
	})
}
