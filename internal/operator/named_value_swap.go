package operator

import (
	"bytes"
	"slices"
)

// namedValueSwap needs the rule in NAMED_VALUE_SWAP.yml to bind the value of each keyed element to $VALUE.
type namedValueSwap struct{}

func (s namedValueSwap) Operator() string {
	return "NAMED_VALUE_SWAP"
}

func (s namedValueSwap) Edits(source []byte, matches []Match) []Edit {
	matches = slices.SortedFunc(slices.Values(matches), func(a, b Match) int { return a.Start - b.Start })
	var edits []Edit
	for i, first := range matches {
		rest := matches[i+1:]
		next, _ := slices.BinarySearchFunc(rest, first.End, func(m Match, end int) int { return m.Start - end })
		if next == len(rest) {
			continue
		}
		second := rest[next]
		firstValue, firstFound := first.Variables["VALUE"]
		secondValue, secondFound := second.Variables["VALUE"]
		if !firstFound || !secondFound || !s.adjacent(source[first.End:second.Start]) {
			continue
		}
		edits = append(edits, Edit{
			File:     first.File,
			Operator: s.Operator(),
			Rule:     first.Rule,
			Start:    firstValue.Start,
			End:      secondValue.End,
			Original: string(source[firstValue.Start:secondValue.End]),
			Replacement: string(source[secondValue.Start:secondValue.End]) +
				string(source[firstValue.End:secondValue.Start]) +
				string(source[firstValue.Start:firstValue.End]),
		})
	}
	return edits
}

func (s namedValueSwap) adjacent(between []byte) bool {
	commas := 0
	for len(between) > 0 {
		switch {
		case between[0] == ',':
			commas++
			between = between[1:]
		case bytes.HasPrefix(between, []byte("//")), between[0] == '#':
			end := bytes.IndexByte(between, '\n')
			if end < 0 {
				return false
			}
			between = between[end:]
		case bytes.HasPrefix(between, []byte("/*")):
			end := bytes.Index(between, []byte("*/"))
			if end < 0 {
				return false
			}
			between = between[end+2:]
		case bytes.ContainsAny(between[:1], " \t\r\n"):
			between = between[1:]
		default:
			return false
		}
	}
	return commas == 1
}
