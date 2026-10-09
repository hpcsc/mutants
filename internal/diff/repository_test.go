//go:build unit

package diff_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

		t.Run("with IgnoreSpaceChange, a line whose only change is the amount of white space does not count", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n\nvar (\n\ta = 1\n\tbb = 2\n)\n\nfunc f() int {\n\treturn a\n}\n")
			r.commit("add a")
			r.write("a.go", "package a\n\nvar (\n\ta  = 1\n\tbb = 3\n)\n\nfunc f() int {\n\t\treturn a \n}\n")

			ignored, err := r.open().Changed(context.Background(), "HEAD", diff.Pathspec{Extensions: []string{".go"}, IgnoreSpaceChange: true})
			require.NoError(t, err)
			counted, err := r.open().Changed(context.Background(), "HEAD", goFiles)
			require.NoError(t, err)

			require.Equal(t, []int{5}, changedLines(t, ignored, "a.go"))
			require.Equal(t, []int{4, 5, 9}, changedLines(t, counted, "a.go"))
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
				"colour":            {"color.diff", "always"},
				"external tool":     {"diff.external", "false"},
			}
			for name, setting := range settings {
				t.Run(name, func(t *testing.T) {
					r := newGitRepository(t)
					// the folder b/ looks like a prefix: without fixed prefixes, the diff loses it
					r.write("b/a.go", "package b\n\nvar a = 1\n")
					r.commit("add a")
					r.write("b/a.go", "package b\n\nvar a = 2\n")
					r.git("config", setting[0], setting[1])

					lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

					require.NoError(t, err)
					require.Equal(t, []string{"b/a.go"}, lines.Files())
					require.Equal(t, []int{3}, changedLines(t, lines, "b/a.go"))
				})
			}
		})

		t.Run("a renamed file marks only its changed lines, under its new name, also when the user turns off renames", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("old.go", "package a\n\nvar a = 1\nvar b = 2\nvar c = 3\n")
			r.commit("add old")
			r.git("config", "diff.renames", "false")
			r.git("mv", "old.go", "new.go")
			r.write("new.go", "package a\n\nvar a = 1\nvar b = 9\nvar c = 3\n")

			lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

			require.NoError(t, err)
			require.Equal(t, []string{"new.go"}, lines.Files())
			require.Equal(t, []int{4}, changedLines(t, lines, "new.go"))
		})

		t.Run("an added line that looks like a file header stays a line of its file", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n\nconst fixture = `\n`\n\nvar a = 1\n")
			r.commit("add a")
			r.write("a.go", "package a\n\nconst fixture = `\n++ b/other.go\n`\n\nvar a = 2\n")

			lines, err := r.open().Changed(context.Background(), "HEAD", goFiles)

			require.NoError(t, err)
			require.Equal(t, []string{"a.go"}, lines.Files())
			require.Equal(t, []int{4, 7}, changedLines(t, lines, "a.go"))
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

		t.Run("skips other extensions and the excluded paths, tracked or untracked", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("notes.md", "notes\n")
			r.write("vendor/lib/lib.go", "package lib\n")
			r.write("api/client_gen.go", "package api\n")
			r.write("api/client.go", "package api\n")
			r.commit("add")
			r.write("notes.md", "more notes\n")
			r.write("vendor/lib/lib.go", "package lib\n\nvar v = 1\n")
			r.write("api/client_gen.go", "package api\n\nvar g = 1\n")
			r.write("api/client.go", "package api\n\nvar c = 1\n")
			r.write("api/new_gen.go", "package api\n")

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

	t.Run("origin head", func(t *testing.T) {
		t.Run("gives the default branch of origin", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("start")
			r.git("update-ref", "refs/remotes/origin/trunk", "HEAD")
			r.git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")

			head, found := r.open().OriginHead(context.Background())

			require.True(t, found)
			require.Equal(t, "origin/trunk", head)
		})

		t.Run("a clone that does not know the default branch of origin gives false", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("start")

			_, found := r.open().OriginHead(context.Background())

			require.False(t, found)
		})
	})

	t.Run("git folder", func(t *testing.T) {
		t.Run("a linked work tree gets a git folder of its own", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("start")
			parent, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			r.git("worktree", "add", "--quiet", filepath.Join(parent, "linked"))
			linked, err := diff.Open(context.Background(), filepath.Join(parent, "linked"))
			require.NoError(t, err)

			folder, err := linked.GitFolder(context.Background())

			require.NoError(t, err)
			require.Equal(t, filepath.Join(r.root, ".git", "worktrees", "linked"), folder)
		})
	})

	t.Run("shared git folder", func(t *testing.T) {
		t.Run("a linked work tree shares the git folder of the main work tree", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("a.go", "package a\n")
			r.commit("start")
			parent, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			r.git("worktree", "add", "--quiet", filepath.Join(parent, "linked"))
			linked, err := diff.Open(context.Background(), filepath.Join(parent, "linked"))
			require.NoError(t, err)

			folder, err := linked.SharedGitFolder(context.Background())

			require.NoError(t, err)
			require.Equal(t, filepath.Join(r.root, ".git"), folder)
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

		t.Run("a folder outside the repository returns an error", func(t *testing.T) {
			r := newGitRepository(t)
			r.write("pkg/a.go", "package pkg\n")
			r.commit("add pkg")
			t.Chdir(t.TempDir())

			_, err := r.open().All(context.Background(), []string{"."}, goFiles)

			require.ErrorContains(t, err, "is not in the repository")
		})
	})
}

func FuzzRepositoryChanged(f *testing.F) {
	f.Add("calc", "a\nb\nc\n", "a\nB\nc\nd\n")
	f.Add("with space", "x\n", "x\ny")
	f.Add(`quote"name`, "", "first\n")
	f.Add("tab\tname", "a\n", "")
	f.Add("ünïcode", "a\r\nb\r\n", "a\r\nc\r\n")
	f.Add(`back\slash`, "same\n", "same\n")
	f.Add("-dash", "+a\n-b\n", "-b\n+a\n")
	f.Fuzz(func(t *testing.T, stem, old, changed string) {
		if stem == "" || len(stem) > 200 || strings.ContainsAny(stem, "/\x00") || !utf8.ValidString(stem) || strings.ContainsRune(old+changed, 0) {
			t.Skip()
		}
		r := newGitRepository(t)
		name := stem + ".go"
		r.write(name, old)
		r.commit("old")
		r.write(name, changed)

		lines, err := r.open().Changed(context.Background(), "HEAD", diff.Pathspec{Extensions: []string{".go"}})

		require.NoError(t, err)
		files := lines.Files()
		require.LessOrEqual(t, len(files), 1, "the diff names more files than the repository has: %q", files)
		for _, file := range files {
			_, err = os.Stat(filepath.Join(r.root, file))
			require.NoError(t, err, "the diff names a file that does not exist: %q", file)
			require.False(t, lines.Touches(file, len(textLines(changed))+1, len(textLines(changed))+100), "a line after the end of the file")
		}
		oldLines := map[string]bool{}
		for _, line := range textLines(old) {
			oldLines[line] = true
		}
		for i, line := range textLines(changed) {
			if !oldLines[line] {
				require.Len(t, files, 1, "line %d, %q, is new", i+1, line)
				require.True(t, lines.Has(files[0], i+1), "line %d, %q, is new", i+1, line)
			}
		}
	})
}

// textLines splits as git does: at each line end, and the last line counts also without a line end.
func textLines(text string) []string {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
