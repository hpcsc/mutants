package report

import (
	"cmp"
	"encoding/json"
	"io"
	"slices"

	"github.com/hpcsc/mutants/internal/mutant"
)

type jsonMutant struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Operator    string `json:"operator"`
	Status      string `json:"status"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
	Detail      string `json:"detail,omitempty"`
}

type jsonReport struct {
	Base    string       `json:"base,omitempty"`
	Mutants []jsonMutant `json:"mutants"`
}

func JSON(w io.Writer, mutants []mutant.Mutant, base string) error {
	report := jsonReport{Base: base, Mutants: []jsonMutant{}}
	sorted := slices.SortedFunc(slices.Values(mutants), func(a, b mutant.Mutant) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Start, b.Start), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	for _, m := range sorted {
		report.Mutants = append(report.Mutants, jsonMutant{
			ID:          m.ID.String(),
			File:        m.File,
			Line:        m.Line,
			Column:      m.Column,
			Operator:    m.Operator,
			Status:      m.Result.Status.String(),
			Original:    m.Original,
			Replacement: m.Replacement,
			Detail:      m.Result.Detail,
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
