//go:build unit

package report_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/report"
	"github.com/stretchr/testify/require"
)

func TestStryker(t *testing.T) {
	t.Run("stryker", func(t *testing.T) {
		t.Run("writes each file with its source, and each mutant with the status names of the schema and its bug", func(t *testing.T) {
			root := t.TempDir()
			source := "package a\n\nfunc f(a, b int) bool {\n\treturn a < b\n}\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte(source), 0o644))
			survivor := reported("a.go", 4, "CONDITIONALS_BOUNDARY", 1, "a < b", "a <= b", mutant.Lived)
			survivor.Column, survivor.EndLine, survivor.EndColumn = 9, 4, 14
			killed := reported("a.go", 4, "PROPOSED", 418273, "a < b", "a >= b", mutant.Killed)
			killed.Column, killed.EndLine, killed.EndColumn = 9, 4, 14
			killed.Bug = "equal values are less"
			var output strings.Builder

			require.NoError(t, report.Stryker(&output, root, "go", []mutant.Mutant{survivor, killed}))

			require.JSONEq(t, `{
				"schemaVersion": "2",
				"thresholds": {"high": 80, "low": 60},
				"projectRoot": "`+root+`",
				"files": {
					"a.go": {
						"language": "go",
						"source": "package a\n\nfunc f(a, b int) bool {\n\treturn a < b\n}\n",
						"mutants": [{
							"id": "a.go:(*Handler).accounts:CONDITIONALS_BOUNDARY#1",
							"mutatorName": "CONDITIONALS_BOUNDARY",
							"replacement": "a <= b",
							"status": "Survived",
							"location": {"start": {"line": 4, "column": 9}, "end": {"line": 4, "column": 14}}
						}, {
							"id": "a.go:(*Handler).accounts:PROPOSED#418273",
							"mutatorName": "PROPOSED",
							"replacement": "a >= b",
							"description": "equal values are less",
							"status": "Killed",
							"location": {"start": {"line": 4, "column": 9}, "end": {"line": 4, "column": 14}}
						}]
					}
				}
			}`, output.String())
		})

		t.Run("names the other statuses the way the schema does, with the detail as the reason", func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644))
			var mutants []mutant.Mutant
			for i, status := range []mutant.Status{mutant.NotCovered, mutant.NotViable, mutant.TimedOut, mutant.InfraError} {
				m := reported("a.go", 1, "BRANCH_IF", i+1, "{ g() }", "{}", status)
				m.Verdict.Detail = fmt.Sprintf("detail %d", i+1)
				mutants = append(mutants, m)
			}
			var output strings.Builder

			require.NoError(t, report.Stryker(&output, root, "go", mutants))

			var document struct {
				Files map[string]struct {
					Mutants []struct{ Status, StatusReason string }
				}
			}
			require.NoError(t, json.Unmarshal([]byte(output.String()), &document))
			require.Equal(t, []struct{ Status, StatusReason string }{
				{"NoCoverage", "detail 1"}, {"CompileError", "detail 2"}, {"Timeout", "detail 3"}, {"RuntimeError", "detail 4"},
			}, document.Files["a.go"].Mutants)
		})
	})
}
