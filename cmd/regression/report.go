package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

const (
	killed     = "KILLED"
	lived      = "LIVED"
	notCovered = "NOT COVERED"
	notViable  = "NOT VIABLE"
	timedOut   = "TIMED OUT"
	infraError = "INFRA ERROR"
)

var statuses = []string{killed, lived, notCovered, notViable, timedOut, infraError}

type report struct {
	Base    string           `json:"base"`
	Mutants []reportedMutant `json:"mutants"`
}

type reportedMutant struct {
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

// applyTo must not use the code of mutants, because the plain tests check the verdicts of that code
func (m reportedMutant) applyTo(source []byte) ([]byte, error) {
	offset := 0
	for line := 1; line < m.Line; line++ {
		next := bytes.IndexByte(source[offset:], '\n')
		if next < 0 {
			return nil, fmt.Errorf("%s has no line %d", m.File, m.Line)
		}
		offset += next + 1
	}
	offset += m.Column - 1
	if m.Column < 1 || offset > len(source) || !bytes.HasPrefix(source[offset:], []byte(m.Original)) {
		return nil, fmt.Errorf("%s:%d:%d does not hold %q", m.File, m.Line, m.Column, m.Original)
	}
	return slices.Concat(source[:offset], []byte(m.Replacement), source[offset+len(m.Original):]), nil
}

func statusOfRerun(stdout string) string {
	for _, status := range statuses {
		if strings.HasPrefix(stdout, status+":") {
			return status
		}
	}
	return ""
}

// sample picks the same mutants in each run of the regression test, so that a reader can run a finding again
func sample(mutants []reportedMutant, status string, count int) []reportedMutant {
	var matching []reportedMutant
	for _, m := range mutants {
		if m.Status == status {
			matching = append(matching, m)
		}
	}
	if len(matching) <= count {
		return matching
	}
	picked := make([]reportedMutant, 0, count)
	for i := range count {
		picked = append(picked, matching[i*len(matching)/count])
	}
	return picked
}
