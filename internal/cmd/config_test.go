//go:build unit

package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func withFlags(t *testing.T, arguments ...string) *cli.Command {
	t.Helper()
	command := &cli.Command{
		Name:      "test",
		Writer:    io.Discard,
		ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "base"},
			&cli.IntFlag{Name: "workers", Value: 4},
			&cli.StringSliceFlag{Name: "tags"},
			&cli.StringSliceFlag{Name: "operators"},
		},
		Action: func(context.Context, *cli.Command) error { return nil },
	}
	require.NoError(t, command.Run(context.Background(), append([]string{"test"}, arguments...)))
	return command
}

func TestConfig(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		t.Run("reads each setting of .mutants.yml", func(t *testing.T) {
			root := t.TempDir()
			content := "base: origin/main\nworkers: 2\ntags: [unit]\noperators: [-ERRORF_WRAP]\nexclude: [\"**/*_gen.go\", \"vendor/**\"]\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte(content), 0o644))

			loaded, err := loadConfig(root)

			require.NoError(t, err)
			require.Equal(t, config{
				Base:      "origin/main",
				Workers:   2,
				Tags:      []string{"unit"},
				Operators: []string{"-ERRORF_WRAP"},
				Exclude:   []string{"**/*_gen.go", "vendor/**"},
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

			require.EqualError(t, err, "unknown key workerz in .mutants.yml (line 2): the keys are base, workers, tags, operators, exclude")
		})

		t.Run("an empty file gives no settings", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants.yml"), []byte("# no settings\n"), 0o644))

			loaded, err := loadConfig(root)

			require.NoError(t, err)
			require.Equal(t, config{}, loaded)
		})
	})

	t.Run("settings", func(t *testing.T) {
		t.Run("a flag wins over the file", func(t *testing.T) {
			loaded := config{Base: "origin/main", Workers: 2, Tags: []string{"unit"}, Operators: []string{"-ERRORF_WRAP"}}
			command := withFlags(t, "--base", "HEAD", "--workers", "8", "--tags", "integration", "--operators", "BRANCH_IF")

			require.Equal(t, "HEAD", loaded.base(command))
			require.Equal(t, 8, loaded.workers(command))
			require.Equal(t, []string{"integration"}, loaded.tags(command))
			require.Equal(t, []string{"BRANCH_IF"}, loaded.operators(command))
		})

		t.Run("the file wins over the default", func(t *testing.T) {
			loaded := config{Base: "origin/main", Workers: 2, Tags: []string{"unit"}, Operators: []string{"-ERRORF_WRAP"}}
			command := withFlags(t)

			require.Equal(t, "origin/main", loaded.base(command))
			require.Equal(t, 2, loaded.workers(command))
			require.Equal(t, []string{"unit"}, loaded.tags(command))
			require.Equal(t, []string{"-ERRORF_WRAP"}, loaded.operators(command))
		})

		t.Run("with no flag and no file, the base is origin/HEAD and there are 4 workers", func(t *testing.T) {
			command := withFlags(t)

			require.Equal(t, "origin/HEAD", config{}.base(command))
			require.Equal(t, 4, config{}.workers(command))
		})

		t.Run("a list of operators keeps the sign of each operator", func(t *testing.T) {
			command := withFlags(t, "--operators=-ERRORF_WRAP,+SWAP_FIELDS")

			require.Equal(t, []string{"-ERRORF_WRAP", "+SWAP_FIELDS"}, config{}.operators(command))
		})
	})
}
