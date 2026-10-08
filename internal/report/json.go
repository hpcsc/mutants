package report

import (
	"cmp"
	"encoding/json"
	"io"
	"slices"

	"github.com/hpcsc/mutants/internal/mutant"
)

type jsonMutant struct {
	ID          string   `json:"id"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Column      int      `json:"column"`
	Operator    string   `json:"operator"`
	Status      string   `json:"status"`
	Original    string   `json:"original"`
	Replacement string   `json:"replacement"`
	Bug         string   `json:"bug,omitempty"`
	Refs        []string `json:"refs,omitempty"`
	Detail      string   `json:"detail,omitempty"`
	Inside      string   `json:"inside,omitempty"`
}

type jsonRejection struct {
	File   string `json:"file"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Bug    string `json:"bug"`
	Ref    string `json:"ref,omitempty"`
	Reason string `json:"reason"`
}

type jsonProposals struct {
	Accepted int             `json:"accepted"`
	Rejected []jsonRejection `json:"rejected"`
}

type jsonCallerGap struct {
	File     string   `json:"file"`
	Function string   `json:"function"`
	Lines    []int    `json:"lines"`
	Callers  []string `json:"callers"`
}

type jsonReport struct {
	Base       string           `json:"base,omitempty"`
	Mutants    []jsonMutant     `json:"mutants"`
	Proposals  *jsonProposals   `json:"proposals,omitempty"`
	CallerGaps *[]jsonCallerGap `json:"callerGaps,omitempty"`
}

func JSON(w io.Writer, outcome Outcome) error {
	mutants, base := outcome.Mutants, outcome.Base
	report := jsonReport{Base: base, Mutants: []jsonMutant{}}
	if outcome.CallerGaps != nil {
		gaps := []jsonCallerGap{}
		for _, gap := range *outcome.CallerGaps {
			gaps = append(gaps, jsonCallerGap{File: gap.File, Function: gap.Function, Lines: gap.Lines, Callers: gap.Callers})
		}
		report.CallerGaps = &gaps
	}
	if outcome.Proposals != nil {
		report.Proposals = &jsonProposals{Accepted: outcome.Proposals.Accepted, Rejected: []jsonRejection{}}
		for _, rejection := range outcome.Proposals.Rejected {
			report.Proposals.Rejected = append(report.Proposals.Rejected, jsonRejection{
				File:   rejection.Proposal.File,
				Old:    rejection.Proposal.Old,
				New:    rejection.Proposal.New,
				Bug:    rejection.Proposal.Bug,
				Ref:    rejection.Proposal.Ref,
				Reason: rejection.Reason,
			})
		}
	}
	sorted := slices.SortedFunc(slices.Values(mutants), func(a, b mutant.Mutant) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Start, b.Start), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	outerOf := insideOf(mutants)
	for _, m := range sorted {
		inside := ""
		if outer, found := outerOf[m.ID]; found {
			inside = outer.String()
		}
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
			Refs:        m.Refs,
			Detail:      m.Verdict.Detail,
			Inside:      inside,
		})
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
