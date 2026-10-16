//go:build unit

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/hpcsc/mutants/internal/language/python"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/run"
	"github.com/stretchr/testify/require"
)

func TestOperators(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		t.Run("lists an operator that the catalog leaves out of a language with the reason, after the operators of the language", func(t *testing.T) {
			root := t.TempDir()
			goPack, err := operator.Load("go", root, "")
			require.NoError(t, err)
			pythonPack, err := operator.Load("python", root, "")
			require.NoError(t, err)
			catalog := operator.Standard()
			catalog["COUNT_ZERO"] = operator.Entry{Tier: operator.Required, LeftOut: map[string]string{"go": "Go has no count", "python": "Python has no count"}}
			var output strings.Builder

			err = writeOperators(&output, []run.Language{
				{Adapter: golang.New(root, golang.Settings{}), Pack: goPack},
				{Adapter: python.New(root, python.Settings{}), Pack: pythonPack},
			}, catalog)

			require.NoError(t, err)
			text := output.String()
			require.Regexp(t, `(?m)^go\s+BRANCH_IF\s+on\s+BRANCH_IF\n(?s:.*)^go\s+COUNT_ZERO\s+-\s+none: Go has no count\n(?s:.*)^python\s+BRANCH_IF\s+on\s+BRANCH_IF\n(?s:.*)^python\s+COUNT_ZERO\s+-\s+none: Python has no count$`, strings.TrimSpace(text))
		})

		t.Run("names the file of a rule that a repository adds", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".mutants/operators/go"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, ".mutants/operators/go/nil_map.yml"), []byte("id: NIL_MAP\nlanguage: go\nrule:\n  pattern: map[$K]$V{}\nfix: nil\n"), 0o644))
			pack, err := operator.Load("go", root, "")
			require.NoError(t, err)
			var output strings.Builder

			err = writeOperators(&output, []run.Language{{Adapter: golang.New(root, golang.Settings{}), Pack: pack}}, operator.Standard())

			require.NoError(t, err)
			require.Regexp(t, `(?m)^go\s+NIL_MAP\s+on\s+NIL_MAP \(from \.mutants/operators/go/nil_map\.yml\)$`, output.String())
			require.Regexp(t, `(?m)^go\s+BRANCH_IF\s+on\s+BRANCH_IF$`, output.String())
		})
	})
}
