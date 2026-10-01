//go:build unit

package report_test

import (
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
		t.Run("writes each file with its source, and each mutant with the status names of the schema", func(t *testing.T) {
			root := t.TempDir()
			source := "package a\n\nfunc f(a, b int) bool {\n\treturn a < b\n}\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte(source), 0o644))
			survivor := reported("a.go", 4, "CONDITIONALS_BOUNDARY", 1, "a < b", "a <= b", mutant.Lived)
			survivor.Column, survivor.EndLine, survivor.EndColumn = 9, 4, 14
			killed := reported("a.go", 4, "CONDITIONALS_NEGATION", 1, "a < b", "a >= b", mutant.Killed)
			killed.Column, killed.EndLine, killed.EndColumn = 9, 4, 14
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
							"id": "a.go:(*Handler).accounts:CONDITIONALS_NEGATION#1",
							"mutatorName": "CONDITIONALS_NEGATION",
							"replacement": "a >= b",
							"status": "Killed",
							"location": {"start": {"line": 4, "column": 9}, "end": {"line": 4, "column": 14}}
						}]
					}
				}
			}`, output.String())
		})
	})
}
