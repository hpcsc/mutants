//go:build unit

package report_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/proposal"
	"github.com/hpcsc/mutants/internal/report"
	"github.com/stretchr/testify/require"
)

func TestJSON(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		t.Run("writes one document with the base and the fields of each mutant", func(t *testing.T) {
			lived := reported("handler.go", 42, "BRANCH_IF", 1, "{ return err }", "{}", mutant.Lived)
			killed := reported("handler.go", 50, "INVERT_LOGICAL", 1, "a && b", "a || b", mutant.Killed)
			killed.Start = 100
			killed.Verdict.Detail = "--- FAIL: TestAccounts"
			var output strings.Builder

			require.NoError(t, report.JSON(&output, report.Outcome{Mutants: []mutant.Mutant{killed, lived}, Base: "1a2b3c4d5e"}))

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

		t.Run("holds the bug of a proposed mutant, and each rejected proposal with its reason", func(t *testing.T) {
			proposed := reported("case.go", 91, "PROPOSED", 418273, "a", "b", mutant.Lived)
			proposed.Bug = "the case never closes"
			rejected := proposal.Rejection{Proposal: proposal.Proposal{File: "case.go", Old: "return", New: "", Bug: "the note is lost"}, Reason: "old found 3 times"}
			var output strings.Builder

			require.NoError(t, report.JSON(&output, report.Outcome{
				Mutants:   []mutant.Mutant{proposed},
				Proposals: &report.Proposals{Accepted: 1, Rejected: []proposal.Rejection{rejected}},
			}))

			require.JSONEq(t, `{
				"mutants": [{
					"id": "case.go:(*Handler).accounts:PROPOSED#418273", "file": "case.go", "line": 91, "column": 2,
					"operator": "PROPOSED", "status": "LIVED", "original": "a", "replacement": "b", "bug": "the case never closes"
				}],
				"proposals": {
					"accepted": 1,
					"rejected": [{"file": "case.go", "old": "return", "new": "", "bug": "the note is lost", "reason": "old found 3 times"}]
				}
			}`, output.String())
		})

		t.Run("holds each caller gap when the run looked for them, also when it found none", func(t *testing.T) {
			gaps := []language.CallerGap{{File: "gate/gate.go", Function: "(*Checker).Allow", Lines: []int{16, 17}, Callers: []string{"handler"}}}
			var found, none strings.Builder

			require.NoError(t, report.JSON(&found, report.Outcome{CallerGaps: &gaps}))
			require.NoError(t, report.JSON(&none, report.Outcome{CallerGaps: &[]language.CallerGap{}}))

			require.JSONEq(t, `{"mutants": [], "callerGaps": [{"file": "gate/gate.go", "function": "(*Checker).Allow", "lines": [16, 17], "callers": ["handler"]}]}`, found.String())
			require.JSONEq(t, `{"mutants": [], "callerGaps": []}`, none.String())
		})

		t.Run("no mutant gives an empty list", func(t *testing.T) {
			var output strings.Builder

			require.NoError(t, report.JSON(&output, report.Outcome{}))

			require.JSONEq(t, `{"mutants": []}`, output.String())
		})
	})
}
