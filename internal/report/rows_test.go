//go:build unit

package report_test

import (
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/proposal"
	"github.com/hpcsc/mutants/internal/report"
	"github.com/stretchr/testify/require"
)

func reported(file string, line int, operator string, number int, original, replacement string, status mutant.Status) mutant.Mutant {
	return mutant.Mutant{
		ID:          mutant.ID{File: file, Function: "(*Handler).accounts", Operator: operator, Number: number},
		File:        file,
		Line:        line,
		Column:      2,
		Operator:    operator,
		Original:    original,
		Replacement: replacement,
		Verdict:     mutant.Verdict{Status: status},
	}
}

func TestRows(t *testing.T) {
	t.Run("rows", func(t *testing.T) {
		t.Run("groups the mutants that need a look by status, and leaves out the killed and the not viable ones", func(t *testing.T) {
			mutants := []mutant.Mutant{
				reported("handler.go", 50, "INVERT_LOGICAL", 1, "a && b", "a || b", mutant.Killed),
				reported("handler.go", 43, "RETURN_ERROR_NIL", 1, "err", "nil", mutant.NotCovered),
				reported("handler.go", 42, "BRANCH_IF", 1, "{ return nil, err }", "{}", mutant.Lived),
				reported("handler.go", 60, "ARITHMETIC_BASE", 1, `"a" + b`, `"a" - b`, mutant.NotViable),
				reported("handler.go", 70, "BREAK_AT_END", 1, "for { f() }", "for { f(); break }", mutant.TimedOut),
				reported("handler.go", 80, "BRANCH_ELSE", 1, "{ g() }", "{}", mutant.InfraError),
			}
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{Mutants: mutants, Base: "1a2b3c4d5e6f7a8b"}))

			require.Equal(t, `LIVED:
  handler.go:42 BRANCH_IF: { return nil, err } -> {}  [handler.go:(*Handler).accounts:BRANCH_IF#1]
NOT COVERED:
  handler.go:43 RETURN_ERROR_NIL: err -> nil  [handler.go:(*Handler).accounts:RETURN_ERROR_NIL#1]
TIMED OUT:
  handler.go:70 BREAK_AT_END: for { f() } -> for { f(); break }  [handler.go:(*Handler).accounts:BREAK_AT_END#1]
INFRA ERROR:
  handler.go:80 BRANCH_ELSE: { g() } -> {}  [handler.go:(*Handler).accounts:BRANCH_ELSE#1]
mutants: 6, killed: 1, lived: 1, not covered: 1, not viable: 1, timed out: 1, infra error: 1 (base 1a2b3c4d5e)
`, output.String())
		})

		t.Run("prints the NOT COVERED mutants that share a reason as one row with their count", func(t *testing.T) {
			noTests := func(m mutant.Mutant) mutant.Mutant {
				m.Verdict.Detail = "package report has no test files"
				return m
			}
			mutants := []mutant.Mutant{
				noTests(reported("report/main.go", 20, "BRANCH_IF", 1, "{ g() }", "{}", mutant.NotCovered)),
				reported("handler.go", 43, "RETURN_ERROR_NIL", 1, "err", "nil", mutant.NotCovered),
				noTests(reported("report/main.go", 10, "RETURN_ZERO", 1, "n", "0", mutant.NotCovered)),
				noTests(reported("report/write.go", 5, "STATEMENT_REMOVE", 1, "close(f)", "", mutant.NotCovered)),
			}
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{Mutants: mutants}))

			require.Equal(t, `NOT COVERED:
  handler.go:43 RETURN_ERROR_NIL: err -> nil  [handler.go:(*Handler).accounts:RETURN_ERROR_NIL#1]
  package report has no test files: 3 mutants
mutants: 4, not covered: 4
`, output.String())
		})

		t.Run("shows code on one line, shortens long code, and shows an empty replacement as (nothing)", func(t *testing.T) {
			mutants := []mutant.Mutant{
				reported("a.go", 3, "BRANCH_CASE", 1, "g()\n\t\th(\"a long argument that goes past the end\")\n", "", mutant.Lived),
			}
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{Mutants: mutants}))

			require.Equal(t, "LIVED:\n  a.go:3 BRANCH_CASE: g() h(\"a long argument that goes past th ... -> (nothing)  [a.go:(*Handler).accounts:BRANCH_CASE#1]\nmutants: 1, lived: 1\n", output.String())
		})

		t.Run("a long mutant starts both texts a few words before the first difference", func(t *testing.T) {
			mutants := []mutant.Mutant{
				reported("a.go", 5, "BREAK_AT_END", 1, "for _, item := range items {\n\t\ttotal += item\n\t}", "for _, item := range items {\n\t\ttotal += item\n\t\tbreak\n\t}", mutant.Lived),
			}
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{Mutants: mutants}))

			require.Equal(t, "LIVED:\n  a.go:5 BREAK_AT_END: ... total += item } -> ... total += item break }  [a.go:(*Handler).accounts:BREAK_AT_END#1]\nmutants: 1, lived: 1\n", output.String())
		})

		t.Run("a proposed mutant shows its bug in place of the code", func(t *testing.T) {
			proposed := reported("case.go", 91, "PROPOSED", 418273, "waited && !note", "waited && open && !note", mutant.Lived)
			proposed.Bug = "a note after the deadline no longer stops the close"
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{Mutants: []mutant.Mutant{proposed}}))

			require.Equal(t, "LIVED:\n  case.go:91 PROPOSED: a note after the deadline no longer stops the close  [case.go:(*Handler).accounts:PROPOSED#418273]\nmutants: 1, lived: 1\n", output.String())
		})

		t.Run("lists each rejected proposal with its reason, and counts the proposals", func(t *testing.T) {
			outcome := report.Outcome{
				Mutants: []mutant.Mutant{reported("case.go", 91, "PROPOSED", 418273, "a", "b", mutant.Killed)},
				Proposals: &report.Proposals{Accepted: 1, Rejected: []proposal.Rejection{
					{Proposal: proposal.Proposal{File: "case.go", Old: "return", New: "", Bug: "the case never closes"}, Reason: "old found 3 times"},
				}},
			}
			var output strings.Builder

			require.NoError(t, report.Rows(&output, outcome))

			require.Equal(t, "REJECTED PROPOSALS:\n  case.go: old found 3 times: the case never closes\nmutants: 1, killed: 1\nproposals: 1 accepted, 1 rejected\n", output.String())
		})

		t.Run("lists each caller gap with its line ranges and its callers, and counts the gaps", func(t *testing.T) {
			gaps := []language.CallerGap{
				{File: "gate/gate.go", Function: "(*Checker).Allow", Lines: []int{12, 13, 16, 17, 18, 21}, Callers: []string{"handler", "wired"}},
			}
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{CallerGaps: &gaps}))

			require.Equal(t, "CALLER GAPS:\n  gate/gate.go:12-13,16-18,21 (*Checker).Allow, not run by the tests of handler, wired\nmutants: 0\ncaller gaps: 1\n", output.String())
		})

		t.Run("a run that looked for caller gaps and found none says so", func(t *testing.T) {
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{CallerGaps: &[]language.CallerGap{}}))

			require.Equal(t, "mutants: 0\ncaller gaps: 0\n", output.String())
		})

		t.Run("no mutant gives only the count", func(t *testing.T) {
			var output strings.Builder

			require.NoError(t, report.Rows(&output, report.Outcome{Base: "1a2b3c4d5e"}))

			require.Equal(t, "mutants: 0 (base 1a2b3c4d5e)\n", output.String())
		})
	})
}
