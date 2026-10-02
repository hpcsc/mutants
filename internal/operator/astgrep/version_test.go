//go:build unit

package astgrep_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hpcsc/mutants/internal/operator/astgrep"
	"github.com/stretchr/testify/require"
)

func pathWithAstGrep(t *testing.T, version string) {
	t.Helper()
	folder := t.TempDir()
	script := "#!/bin/sh\necho 'ast-grep " + version + "'\n"
	require.NoError(t, os.WriteFile(filepath.Join(folder, "ast-grep"), []byte(script), 0o755))
	t.Setenv("PATH", folder)
}

func TestCheckVersion(t *testing.T) {
	t.Run("check", func(t *testing.T) {
		t.Run("the minimum version and later versions pass", func(t *testing.T) {
			for _, version := range []string{astgrep.MinimumVersion, "0.45.3", "0.46.0", "0.100.0", "1.0.0"} {
				pathWithAstGrep(t, version)

				require.NoError(t, astgrep.CheckVersion(context.Background()), version)
			}
		})

		t.Run("an older version returns an error that names both versions", func(t *testing.T) {
			pathWithAstGrep(t, "0.39.5")

			err := astgrep.CheckVersion(context.Background())

			require.ErrorContains(t, err, "needs ast-grep "+astgrep.MinimumVersion+" or later, and the PATH has 0.39.5")
		})

		t.Run("compares versions by number and not by text, and refuses a version with fewer parts", func(t *testing.T) {
			for _, version := range []string{"0.9.9", "0.45"} {
				pathWithAstGrep(t, version)

				require.ErrorContains(t, astgrep.CheckVersion(context.Background()), "the PATH has "+version, version)
			}
		})

		t.Run("no ast-grep on the PATH returns an error", func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())

			err := astgrep.CheckVersion(context.Background())

			require.ErrorContains(t, err, "needs ast-grep "+astgrep.MinimumVersion+" or later on the PATH")
		})
	})
}
