//go:build unit

package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// parsed runs a real command with no action, so that a test reads the flags that the command declares.
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
			content := "base: origin/main\nworkers: 2\ntags: [unit]\noperators: [-ERRORF_WRAP]\nexclude: [\"**/*_gen.go\", \"vendor/**\"]\n" +
				"zero_functions: [maybe.None]\ncaller_gaps: true\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(content), 0o644))

			loaded, err := loadConfig(root)

			require.NoError(t, err)
			require.Equal(t, config{
				Base:          "origin/main",
				Workers:       2,
				Tags:          []string{"unit"},
				Operators:     []string{"-ERRORF_WRAP"},
				Exclude:       []string{"**/*_gen.go", "vendor/**"},
				ZeroFunctions: []string{"maybe.None"},
				CallerGaps:    true,
			}, loaded)
		})

		t.Run("no file gives no settings", func(t *testing.T) {
			loaded, err := loadConfig(t.TempDir())

			require.NoError(t, err)
			require.Equal(t, config{}, loaded)
		})

		t.Run("an unknown key returns an error that names it, its line and the keys", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("base: origin/main\nworkerz: 2\n"), 0o644))

			_, err := loadConfig(root)

			require.EqualError(t, err, "unknown key workerz in .mutants.yml (line 2): the keys are base, workers, tags, operators, exclude, zero_functions, caller_gaps")
		})

		for _, scenario := range []struct{ name, content, message string }{
			{"a value of the wrong type returns an error that names its line", "workers: four\n", "line 1: cannot unmarshal !!str `four` into int"},
			{"a file that is not keys and values returns an error that lists the keys", "- base\n", ".mutants.yml must hold keys and values"},
			{"a file that is not YAML returns an error", "base: [origin/main\n", "read .mutants.yml: yaml: line 1"},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(scenario.content), 0o644))

				_, err := loadConfig(root)

				require.ErrorContains(t, err, scenario.message)
			})
		}

		t.Run("an empty file gives no settings", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("# no settings\n"), 0o644))

			loaded, err := loadConfig(root)

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
			{"with one tag, sets the base and the tag", "origin/main", map[string]int{"unit": 3}, config{Base: "origin/main", Tags: []string{"unit"}}},
			{"with more than one tag, sets the base and no tag", "origin/main", map[string]int{"unit": 3, "integration": 1}, config{Base: "origin/main"}},
			{"with no default branch of origin and no tag, keeps each default", "", nil, config{}},
		} {
			t.Run(scenario.name+", names each key, and loads with no error", func(t *testing.T) {
				root := t.TempDir()
				text := configTemplate(scenario.base, scenario.tags)
				require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(text), 0o644))

				loaded, err := loadConfig(root)

				require.NoError(t, err)
				require.Equal(t, scenario.want, loaded)
				for _, key := range (config{}).keys() {
					require.True(t, strings.Contains(text, "\n"+key+":") || strings.Contains(text, "\n# "+key+":"), key)
				}
			})
		}

		t.Run("with more than one tag, counts the files of each", func(t *testing.T) {
			text := configTemplate("origin/main", map[string]int{"unit": 3, "integration": 1})

			require.Contains(t, text, "#   integration: 1 file\n#   unit: 3 files\n")
		})

		t.Run("each commented setting loads when it is uncommented, and a commented default is the real default", func(t *testing.T) {
			root := t.TempDir()
			commented := regexp.MustCompile(`(?m)^# (` + strings.Join((config{}).keys(), "|") + `): `)
			text := commented.ReplaceAllString(configTemplate("", nil), "$1: ")
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(text), 0o644))

			loaded, err := loadConfig(root)

			require.NoError(t, err)
			command := parsed(t, newRunCommand())
			require.Equal(t, config{}.base(command), loaded.base(command))
			require.Equal(t, config{}.workers(command), loaded.workers(command))
			require.Equal(t, config{}.callerGaps(command), loaded.callerGaps(command))
		})
	})

	t.Run("settings", func(t *testing.T) {
		t.Run("a flag of run wins over the file, and keeps the sign of each operator", func(t *testing.T) {
			loaded := config{Base: "origin/main", Workers: 2, Tags: []string{"unit"}, Operators: []string{"-ERRORF_WRAP"}, CallerGaps: true}
			command := parsed(t, newRunCommand(), "--base", "HEAD", "--workers", "8", "--tags", "integration", "--operators=-ERRORF_WRAP,+SWAP_FIELDS", "--caller-gaps=false")

			require.Equal(t, "HEAD", loaded.base(command))
			require.Equal(t, 8, loaded.workers(command))
			require.Equal(t, []string{"integration"}, loaded.tags(command))
			require.Equal(t, []string{"-ERRORF_WRAP", "+SWAP_FIELDS"}, loaded.operators(command))
			require.False(t, loaded.callerGaps(command))
		})

		t.Run("a flag of rerun wins over the file", func(t *testing.T) {
			loaded := config{Tags: []string{"unit"}}

			command := parsed(t, newRerunCommand(), "--tags", "integration")

			require.Equal(t, []string{"integration"}, loaded.tags(command))
		})

		t.Run("the file wins over the default", func(t *testing.T) {
			loaded := config{Base: "origin/main", Workers: 2, Tags: []string{"unit"}, Operators: []string{"-ERRORF_WRAP"}, CallerGaps: true}
			command := parsed(t, newRunCommand())

			require.Equal(t, "origin/main", loaded.base(command))
			require.Equal(t, 2, loaded.workers(command))
			require.Equal(t, []string{"unit"}, loaded.tags(command))
			require.Equal(t, []string{"-ERRORF_WRAP"}, loaded.operators(command))
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
