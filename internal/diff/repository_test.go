//go:build unit

package diff_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/stretchr/testify/require"
)

var goFiles = diff.Pathspec{Extensions: []string{".go"}}

type gitRepository struct {
	t    *testing.T
	root string
}

func newGitRepository(t *testing.T) *gitRepository {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	r := &gitRepository{t: t, root: root}
	r.git("init", "--quiet", "--initial-branch=main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "test")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *gitRepository) git(arguments ...string) string {
	r.t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = r.root
	output, err := command.CombinedOutput()
	require.NoError(r.t, err, string(output))
	return strings.TrimSpace(string(output))
}

func (r *gitRepository) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.root, name)
	require.NoError(r.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(r.t, os.WriteFile(path, []byte(content), 0o644))
}

func (r *gitRepository) commit(message string) string {
	r.t.Helper()
	r.git("add", "--all")
	r.git("commit", "--quiet", "--message", message)
	return r.git("rev-parse", "HEAD")
}

func (r *gitRepository) open() *diff.Repository {
	r.t.Helper()
	repository, err := diff.Open(context.Background(), r.root)
	require.NoError(r.t, err)
	return repository
}

func changedLines(t *testing.T, lines diff.Lines, file string) []int {
	t.Helper()
	var numbers []int
	for line := 1; line <= 100; line++ {
		if lines.Has(file, line) {
			numbers = append(numbers, line)
		}
	}
	return numbers
}

func TestRepository(t *testing.T) {
	t.Run("changed", func(t *testing.T) {
		t.Run("reads the lines that a commit on the branch adds or changes", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n\nvar a = 1\n\nvar keep = 0\n")
			r.commit("add a")
			r.git("switch", "--quiet", "--create", "feature")
			r.write("a.go", "package a\n\nvar a = 2\nvar b = 3\n\nvar keep = 0\n")
			r.commit("change a")

			lines, err := r.open().Changed(context.Background(), "main", goFiles)

			require.NoError(t, err)
			require.Equal(t, []string{"a.go"}, lines.Files())
			require.Equal(t, []int{3, 4}, changedLines(t, lines, "a.go"))
		})

		t.Run("reads lines that are not committed", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n\nvar a = 1\n")
			r.commit("add a")
			r.write("a.go", "package a\n\nvar a = 1\nvar b = 2\n")

			lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

			require.NoError(t, err)
			require.Equal(t, []int{4}, changedLines(t, lines, "a.go"))
		})

		t.Run("every line of an untracked file counts, and the index stays the same", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("add a")
			r.write("pkg/new.go", "package pkg\n\nvar n = 1")
			statusBefore := r.git("status", "--porcelain")

			lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

			require.NoError(t, err)
			require.Equal(t, []int{1, 2, 3}, changedLines(t, lines, "pkg/new.go"))
			require.Equal(t, statusBefore, r.git("status", "--porcelain"))
		})

		t.Run("ignores the commits that land on the base after the branch leaves it", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n\nvar a = 1\n")
			r.commit("add a")
			r.git("switch", "--quiet", "--create", "feature")
			r.write("b.go", "package a\n\nvar b = 1\n")
			r.commit("add b")
			r.git("switch", "--quiet", "main")
			r.write("c.go", "package a\n\nvar c = 1\n")
			r.commit("add c")
			r.git("switch", "--quiet", "feature")

			lines, err := r.open().Changed(context.Background(), "main", goFiles)

			require.NoError(t, err)
			require.Equal(t, []string{"b.go"}, lines.Files())
		})

		t.Run("gives the same lines whatever the diff settings of the user are", func(t *testing.T) {
			settings := map[string][]string{
				"mnemonic prefixes": {"diff.mnemonicPrefix", "true"},
				"no prefixes":       {"diff.noprefix", "true"},
				"relative paths":    {"diff.relative", "true"},
				"no renames":        {"diff.renames", "false"},
				"colour":            {"color.diff", "always"},
				"external tool":     {"diff.external", "false"},
			}
			for name, setting := range settings {
				t.Run(name, func(t *testing.T) {
					r := newGitRepository(t)
					r.write("pkg/a.go", "package pkg\n\nvar a = 1\n")
					r.commit("add a")
					r.write("pkg/a.go", "package pkg\n\nvar a = 2\n")
					r.git("config", setting[0], setting[1])

					lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

					require.NoError(t, err)
					require.Equal(t, []string{"pkg/a.go"}, lines.Files())
					require.Equal(t, []int{3}, changedLines(t, lines, "pkg/a.go"))
				})
			}
		})

		t.Run("a file name with a space, a quote or a letter outside ASCII keeps its name", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("add a")
			for _, name := range []string{"with space.go", `with"quote.go`, "café.go"} {
				r.write(name, "package a\n")
				r.git("add", name)
			}

			lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

			require.NoError(t, err)
			require.ElementsMatch(t, []string{"with space.go", `with"quote.go`, "café.go"}, lines.Files())
		})

		t.Run("a removed line marks no line", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n\nvar a = 1\nvar b = 2\n")
			r.commit("add a")
			r.write("a.go", "package a\n\nvar a = 1\n")

			lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

			require.NoError(t, err)
			require.Zero(t, lines.Len())
		})

		t.Run("skips other extensions and the excluded paths", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("add a")
			r.write("notes.md", "notes\n")
			r.write("vendor/lib/lib.go", "package lib\n")
			r.write("api/client_gen.go", "package api\n")
			r.write("api/client.go", "package api\n")

			lines, err := r.open().Changed(context.Background(), "HEAD", diff.Pathspec{
				Extensions: []string{".go"},
				Exclude:    []string{"vendor/**", "**/*_gen.go"},
			})

			require.NoError(t, err)
			require.Equal(t, []string{"api/client.go"}, lines.Files())
		})

		t.Run("an unknown base returns an error that names it", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("add a")

			_, err := r.open().Changed(context.Background(), "origin/HEAD", goFiles)

			require.ErrorContains(t, err, "origin/HEAD")
		})
	})

	t.Run("merge base", func(t *testing.T) {
		t.Run("gives the commit where the branch leaves the base", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			fork := r.commit("add a")
			r.git("switch", "--quiet", "--create", "feature")
			r.write("b.go", "package a\n")
			r.commit("add b")
			r.git("switch", "--quiet", "main")
			r.write("c.go", "package a\n")
			r.commit("add c")
			r.git("switch", "--quiet", "feature")

			base, err := r.open().MergeBase(context.Background(), "main")

			require.NoError(t, err)
			require.Equal(t, fork, base)
		})
	})

	t.Run("all", func(t *testing.T) {
		t.Run("reads every line of the files in a folder, and not in its subfolders", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("pkg/a.go", "package pkg\n\nvar a = 1\n")
			r.write("pkg/sub/b.go", "package sub\n")
			r.commit("add pkg")
			r.write("pkg/new.go", "package pkg\n")
			t.Chdir(r.root)

			lines, err := r.open().All(context.Background(), []string{"pkg"}, goFiles)

			require.NoError(t, err)
			require.Equal(t, []string{"pkg/a.go", "pkg/new.go"}, lines.Files())
			require.Equal(t, []int{1, 2, 3}, changedLines(t, lines, "pkg/a.go"))
		})

		t.Run("a folder that ends in /... also reads its subfolders", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("pkg/a.go", "package pkg\n")
			r.write("pkg/sub/b.go", "package sub\n")
			r.write("other/c.go", "package other\n")
			r.commit("add pkg")
			t.Chdir(filepath.Join(r.root, "pkg"))

			lines, err := r.open().All(context.Background(), []string{"./..."}, goFiles)

			require.NoError(t, err)
			require.Equal(t, []string{"pkg/a.go", "pkg/sub/b.go"}, lines.Files())
		})

		t.Run("a path that is not a folder returns an error", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("pkg/a.go", "package pkg\n")
			r.commit("add pkg")
			t.Chdir(r.root)

			_, err := r.open().All(context.Background(), []string{"pkg/a.go"}, goFiles)

			require.ErrorContains(t, err, "pkg/a.go is not a folder")
		})
	})
}
