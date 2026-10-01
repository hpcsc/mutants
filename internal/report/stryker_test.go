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
			start := strings.Index(source, "a < b")
			survivor := reported("a.go", 4, "CONDITIONALS_BOUNDARY", 1, "a < b", "a <= b", mutant.Lived)
			survivor.Column, survivor.Start, survivor.End = 9, start, start+len("a < b")
			var output strings.Builder

			require.NoError(t, report.Stryker(&output, root, "go", []mutant.Mutant{survivor}))

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
						}]
					}
				}
			}`, output.String())
		})
	})
}
