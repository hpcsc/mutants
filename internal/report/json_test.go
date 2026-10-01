//go:build unit

package report_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/report"
	"github.com/stretchr/testify/require"
)

func TestJSON(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		t.Run("writes one document with the base and the fields of each mutant", func(t *testing.T) {
			lived := reported("handler.go", 42, "BRANCH_IF", 1, "{ return err }", "{}", mutant.Lived)
			killed := reported("handler.go", 50, "INVERT_LOGICAL", 1, "a && b", "a || b", mutant.Killed)
			killed.Start = 100
			killed.Result.Detail = "--- FAIL: TestAccounts"
			var output strings.Builder

			require.NoError(t, report.JSON(&output, []mutant.Mutant{killed, lived}, "1a2b3c4d5e"))

			var document map[string]any
			require.NoError(t, json.Unmarshal([]byte(output.String()), &document))
			require.Equal(t, map[string]any{
				"base": "1a2b3c4d5e",
				"mutants": []any{
					map[string]any{
						"id": "handler.go:(*Handler).accounts:BRANCH_IF#1", "file": "handler.go", "line": float64(42), "column": float64(2),
						"operator": "BRANCH_IF", "status": "LIVED", "original": "{ return err }", "replacement": "{}",
					},
					map[string]any{
						"id": "handler.go:(*Handler).accounts:INVERT_LOGICAL#1", "file": "handler.go", "line": float64(50), "column": float64(2),
						"operator": "INVERT_LOGICAL", "status": "KILLED", "original": "a && b", "replacement": "a || b",
						"detail": "--- FAIL: TestAccounts",
					},
				},
			}, document)
		})

		t.Run("no mutant gives an empty list", func(t *testing.T) {
			var output strings.Builder

			require.NoError(t, report.JSON(&output, nil, ""))

			require.JSONEq(t, `{"mutants": []}`, output.String())
		})
	})
}
