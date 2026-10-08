//go:build unit

package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func parsed(t *testing.T, command *cli.Command, arguments ...string) *cli.Command {
	t.Helper()
	command.Writer, command.ErrWriter = io.Discard, io.Discard
	command.Action = func(context.Context, *cli.Command) error { return nil }
	command.ExitErrHandler = func(context.Context, *cli.Command, error) {}
	require.NoError(t, command.Run(context.Background(), append([]string{command.Name}, arguments...)))
	return command
}

func TestConfig(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		t.Run("reads each setting of .mutants.yml", func(t *testing.T) {
			root := t.TempDir()
			content := "base: origin/main\nworkers: 2\noperators: [-ERROR_CAUSE_REMOVE]\nexclude: [\"**/*_gen.go\", \"vendor/**\"]\ncaller_gaps: true\n" +
				"go:\n  tags: [unit]\n  zero_functions: [maybe.None]\n  exclude_types: [Fake*]\npython:\n  command: [uv, run, python]\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(content), 0o644))

			loaded, err := loadConfig(root, t.TempDir(), io.Discard)

			require.NoError(t, err)
			require.Equal(t, config{
				Base:       "origin/main",
				Workers:    2,
				Operators:  []string{"-ERROR_CAUSE_REMOVE"},
				Exclude:    []string{"**/*_gen.go", "vendor/**"},
				CallerGaps: true,
				Go:         goConfig{Tags: []string{"unit"}, ZeroFunctions: []string{"maybe.None"}, ExcludeTypes: []string{"Fake*"}},
				Python:     pythonConfig{Command: []string{"uv", "run", "python"}},
			}, loaded)
		})

		t.Run("no file gives no settings", func(t *testing.T) {
			loaded, err := loadConfig(t.TempDir(), t.TempDir(), io.Discard)

			require.NoError(t, err)
			require.Equal(t, config{}, loaded)
		})

		t.Run("an unknown key returns an error that names it, its line and the keys", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("base: origin/main\nworkerz: 2\n"), 0o644))

			_, err := loadConfig(root, t.TempDir(), io.Discard)

			require.EqualError(t, err, "unknown key workerz in .mutants.yml (line 2): the keys are base, workers, operators, exclude, caller_gaps, go, python")
		})

		t.Run("an unknown key of a language returns an error that names it with its language, its line and the keys of the language", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("base: origin/main\ngo:\n  tagz: [unit]\n"), 0o644))

			_, err := loadConfig(root, t.TempDir(), io.Discard)

			require.EqualError(t, err, "unknown key go.tagz in .mutants.yml (line 3): the keys of go are tags, zero_functions, exclude_types")
		})

		for _, scenario := range []struct{ name, content, message string }{
			{"a value of the wrong type returns an error that names its line", "workers: four\n", "line 1: cannot unmarshal !!str `four` into int"},
			{"a file that is not keys and values returns an error that lists the keys", "- base\n", ".mutants.yml must hold keys and values"},
			{"a language that is not keys and values returns an error that names its line", "go: [unit]\n", "go in .mutants.yml (line 1) must hold keys and values, and the keys are tags, zero_functions, exclude_types"},
			{"a file that is not YAML returns an error", "base: [origin/main\n", "read .mutants.yml: yaml: line 1"},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(scenario.content), 0o644))

				_, err := loadConfig(root, t.TempDir(), io.Discard)

				require.ErrorContains(t, err, scenario.message)
			})
		}

		t.Run("without .mutants.yml, reads mutants.yml in the shared git folder", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "mutants.yml"), []byte("base: origin/main\n"), 0o644))

			loaded, err := loadConfig(root, filepath.Join(root, ".git"), io.Discard)

			require.NoError(t, err)
			require.Equal(t, config{Base: "origin/main"}, loaded)
		})

		t.Run("with both files, reads only .mutants.yml, and says on stderr that it ignores mutants.yml", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("workers: 2\n"), 0o644))
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "mutants.yml"), []byte("base: origin/main\n"), 0o644))
			var stderr strings.Builder

			loaded, err := loadConfig(root, filepath.Join(root, ".git"), &stderr)

			require.NoError(t, err)
			require.Equal(t, config{Workers: 2}, loaded)
			require.Equal(t, "mutants ignores .git/mutants.yml, because .mutants.yml exists\n", stderr.String())
		})

		t.Run("with one file, says nothing on stderr", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("workers: 2\n"), 0o644))
			var stderr strings.Builder

			_, err := loadConfig(root, t.TempDir(), &stderr)

			require.NoError(t, err)
			require.Empty(t, stderr.String())
		})

		t.Run("an error in mutants.yml names it from the root when the shared git folder is in the root", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "mutants.yml"), []byte("workerz: 2\n"), 0o644))

			_, err := loadConfig(root, filepath.Join(root, ".git"), io.Discard)

			require.ErrorContains(t, err, "unknown key workerz in .git/mutants.yml (line 1)")
		})

		t.Run("an error in mutants.yml names its full path when the shared git folder is outside the root, as for a linked work tree", func(t *testing.T) {
			sharedGitFolder := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(sharedGitFolder, "mutants.yml"), []byte("go: [unit]\n"), 0o644))

			_, err := loadConfig(t.TempDir(), sharedGitFolder, io.Discard)

			require.ErrorContains(t, err, "go in "+filepath.Join(sharedGitFolder, "mutants.yml")+" (line 1) must hold keys and values")
		})

		t.Run("an empty file gives no settings", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("# no settings\n"), 0o644))

			loaded, err := loadConfig(root, t.TempDir(), io.Discard)

			require.NoError(t, err)
			require.Equal(t, config{}, loaded)
		})
	})

	t.Run("template", func(t *testing.T) {
		for _, scenario := range []struct {
			name string
			base string
			tags map[string]int
			want config
		}{
			{"with one tag, sets the base and the tag", "origin/main", map[string]int{"unit": 3}, config{Base: "origin/main", Go: goConfig{Tags: []string{"unit"}}}},
			{"with more than one tag, sets the base and no tag", "origin/main", map[string]int{"unit": 3, "integration": 1}, config{Base: "origin/main"}},
			{"with no default branch of origin and no tag, keeps each default", "", nil, config{}},
		} {
			t.Run(scenario.name+", names each key, and loads with no error", func(t *testing.T) {
				root := t.TempDir()
				text := configTemplate(scenario.base, scenario.tags)
				require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(text), 0o644))

				loaded, err := loadConfig(root, t.TempDir(), io.Discard)

				require.NoError(t, err)
				require.Equal(t, scenario.want, loaded)
				for _, key := range keysOf(reflect.TypeFor[config]()) {
					require.True(t, strings.Contains(text, "\n"+key+":") || strings.Contains(text, "\n# "+key+":"), key)
				}
				for _, key := range append(keysOf(reflect.TypeFor[goConfig]()), keysOf(reflect.TypeFor[pythonConfig]())...) {
					require.True(t, strings.Contains(text, "\n  "+key+":") || strings.Contains(text, "\n  # "+key+":"), key)
				}
			})
		}

		t.Run("with more than one tag, counts the files of each", func(t *testing.T) {
			text := configTemplate("origin/main", map[string]int{"unit": 3, "integration": 1})

			require.Contains(t, text, "  #   integration: 1 file\n  #   unit: 3 files\n")
		})

		t.Run("each commented setting loads when it is uncommented, and a commented default is the real default", func(t *testing.T) {
			root := t.TempDir()
			keys := append(append(keysOf(reflect.TypeFor[config]()), keysOf(reflect.TypeFor[goConfig]())...), keysOf(reflect.TypeFor[pythonConfig]())...)
			commented := regexp.MustCompile(`(?m)^( *)# (` + strings.Join(keys, "|") + `): `)
			text := commented.ReplaceAllString(configTemplate("", nil), "$1$2: ")
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(text), 0o644))

			loaded, err := loadConfig(root, t.TempDir(), io.Discard)

			require.NoError(t, err)
			command := parsed(t, newRunCommand())
			require.Equal(t, config{}.base(command), loaded.base(command))
			require.Equal(t, config{}.workers(command), loaded.workers(command))
			require.Equal(t, config{}.callerGaps(command), loaded.callerGaps(command))
		})
	})

	t.Run("settings", func(t *testing.T) {
		t.Run("a flag of run wins over the file, and keeps the sign of each operator", func(t *testing.T) {
			loaded := config{Base: "origin/main", Workers: 2, Operators: []string{"-ERROR_CAUSE_REMOVE"}, CallerGaps: true, Go: goConfig{Tags: []string{"unit"}}}
			command := parsed(t, newRunCommand(), "--base", "HEAD", "--workers", "8", "--tags", "integration", "--operators=-ERROR_CAUSE_REMOVE,+NAMED_VALUE_SWAP", "--caller-gaps=false")

			require.Equal(t, "HEAD", loaded.base(command))
			require.Equal(t, 8, loaded.workers(command))
			require.Equal(t, []string{"integration"}, loaded.tags(command))
			require.Equal(t, []string{"-ERROR_CAUSE_REMOVE", "+NAMED_VALUE_SWAP"}, loaded.operators(command))
			require.False(t, loaded.callerGaps(command))
		})

		t.Run("a flag of rerun wins over the file", func(t *testing.T) {
			loaded := config{Go: goConfig{Tags: []string{"unit"}}}

			command := parsed(t, newRerunCommand(), "--tags", "integration")

			require.Equal(t, []string{"integration"}, loaded.tags(command))
		})

		t.Run("the file wins over the default", func(t *testing.T) {
			loaded := config{Base: "origin/main", Workers: 2, Operators: []string{"-ERROR_CAUSE_REMOVE"}, CallerGaps: true, Go: goConfig{Tags: []string{"unit"}}}
			command := parsed(t, newRunCommand())

			require.Equal(t, "origin/main", loaded.base(command))
			require.Equal(t, 2, loaded.workers(command))
			require.Equal(t, []string{"unit"}, loaded.tags(command))
			require.Equal(t, []string{"-ERROR_CAUSE_REMOVE"}, loaded.operators(command))
			require.True(t, loaded.callerGaps(command))
		})

		t.Run("with no flag and no file, the base is origin/HEAD, there are 4 workers, and no check for caller gaps", func(t *testing.T) {
			command := parsed(t, newRunCommand())

			require.Equal(t, "origin/HEAD", config{}.base(command))
			require.Equal(t, 4, config{}.workers(command))
			require.False(t, config{}.callerGaps(command))
		})
	})
}
