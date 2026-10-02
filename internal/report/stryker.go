package report

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/hpcsc/mutants/internal/mutant"
)

type strykerPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type strykerMutant struct {
	ID           string `json:"id"`
	MutatorName  string `json:"mutatorName"`
	Replacement  string `json:"replacement"`
	Description  string `json:"description,omitempty"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason,omitempty"`
	Location     struct {
		Start strykerPosition `json:"start"`
		End   strykerPosition `json:"end"`
	} `json:"location"`
}

type strykerFile struct {
	Language string          `json:"language"`
	Source   string          `json:"source"`
	Mutants  []strykerMutant `json:"mutants"`
}

type strykerReport struct {
	SchemaVersion string `json:"schemaVersion"`
	Thresholds    struct {
		High int `json:"high"`
		Low  int `json:"low"`
	} `json:"thresholds"`
	ProjectRoot string                 `json:"projectRoot"`
	Files       map[string]strykerFile `json:"files"`
}

var strykerStatuses = map[mutant.Status]string{
	mutant.Killed:     "Killed",
	mutant.Lived:      "Survived",
	mutant.NotCovered: "NoCoverage",
	mutant.NotViable:  "CompileError",
	mutant.TimedOut:   "Timeout",
	mutant.InfraError: "RuntimeError",
}

func Stryker(w io.Writer, root string, mutants []mutant.Mutant) error {
	report := strykerReport{SchemaVersion: "2", ProjectRoot: root, Files: map[string]strykerFile{}}
	report.Thresholds.High, report.Thresholds.Low = 80, 60
	for _, m := range mutants {
		file, found := report.Files[m.File]
		if !found {
			source, err := os.ReadFile(filepath.Join(root, m.File))
			if err != nil {
				return err
			}
			file = strykerFile{Language: m.Language, Source: string(source)}
		}
		entry := strykerMutant{
			ID:           m.ID.String(),
			MutatorName:  m.Operator,
			Replacement:  m.Replacement,
			Description:  m.Bug,
			Status:       strykerStatuses[m.Verdict.Status],
			StatusReason: m.Verdict.Detail,
		}
		entry.Location.Start = strykerPosition{Line: m.Line, Column: m.Column}
		entry.Location.End = strykerPosition{Line: m.EndLine, Column: m.EndColumn}
		file.Mutants = append(file.Mutants, entry)
		report.Files[m.File] = file
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
