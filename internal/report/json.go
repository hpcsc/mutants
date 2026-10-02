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
	Bug         string `json:"bug,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type jsonRejection struct {
	File   string `json:"file"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Bug    string `json:"bug"`
	Reason string `json:"reason"`
}

type jsonProposals struct {
	Accepted int             `json:"accepted"`
	Rejected []jsonRejection `json:"rejected"`
}

type jsonReport struct {
	Base      string         `json:"base,omitempty"`
	Mutants   []jsonMutant   `json:"mutants"`
	Proposals *jsonProposals `json:"proposals,omitempty"`
}

func JSON(w io.Writer, outcome Outcome) error {
	mutants, base := outcome.Mutants, outcome.Base
	report := jsonReport{Base: base, Mutants: []jsonMutant{}}
	if outcome.Proposals != nil {
		report.Proposals = &jsonProposals{Accepted: outcome.Proposals.Accepted, Rejected: []jsonRejection{}}
		for _, rejection := range outcome.Proposals.Rejected {
			report.Proposals.Rejected = append(report.Proposals.Rejected, jsonRejection{
				File:   rejection.Proposal.File,
				Old:    rejection.Proposal.Old,
				New:    rejection.Proposal.New,
				Bug:    rejection.Proposal.Bug,
				Reason: rejection.Reason,
			})
		}
	}
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
			Status:      m.Verdict.Status.String(),
			Original:    m.Original,
			Replacement: m.Replacement,
			Bug:         m.Bug,
			Detail:      m.Verdict.Detail,
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
