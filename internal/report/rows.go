package report

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/proposal"
)

const (
	shortText    = 40
	shortContext = 12
)

// Outcome is what one run gives to the reports. Proposals is nil when the run got no proposals, and
// CallerGaps is nil when the run did not look for caller gaps.
type Outcome struct {
	Base       string
	Mutants    []mutant.Mutant
	Proposals  *proposal.Summary
	CallerGaps *[]language.CallerGap
}

func Rows(w io.Writer, outcome Outcome) error {
	mutants := outcome.Mutants
	sorted := slices.SortedFunc(slices.Values(mutants), func(a, b mutant.Mutant) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	reasons := map[string]int{}
	for _, m := range mutants {
		if m.Verdict.Status == mutant.NotCovered && m.Verdict.Detail != "" {
			reasons[m.Verdict.Detail]++
		}
	}
	printed := map[string]bool{}
	var text strings.Builder
	for _, status := range []mutant.Status{mutant.Lived, mutant.NotCovered, mutant.TimedOut, mutant.InfraError} {
		header := false
		for _, m := range sorted {
			if m.Verdict.Status != status {
				continue
			}
			if !header {
				fmt.Fprintf(&text, "%s:\n", status)
				header = true
			}
			reason := m.Verdict.Detail
			switch {
			case status != mutant.NotCovered || reason == "":
				fmt.Fprintf(&text, "  %s\n", Row(m))
			case !printed[reason]:
				fmt.Fprintf(&text, "  %s: %s\n", reason, plural(reasons[reason], "mutant"))
				printed[reason] = true
			}
		}
	}
	if outcome.Proposals != nil && len(outcome.Proposals.Rejected) > 0 {
		text.WriteString("REJECTED PROPOSALS:\n")
		for _, rejection := range outcome.Proposals.Rejected {
			fmt.Fprintf(&text, "  %s: %s: %s\n", rejection.Proposal.File, rejection.Reason, rejection.Proposal.Bug)
		}
	}
	if outcome.CallerGaps != nil && len(*outcome.CallerGaps) > 0 {
		text.WriteString("CALLER GAPS:\n")
		for _, gap := range *outcome.CallerGaps {
			fmt.Fprintf(&text, "  %s:%s %s, not run by the tests of %s\n", gap.File, lineRanges(gap.Lines), gap.Function, strings.Join(gap.Callers, ", "))
		}
	}
	text.WriteString(Counts(mutants, outcome.Base) + "\n")
	if outcome.Proposals != nil {
		fmt.Fprintf(&text, "proposals: %d accepted, %d rejected\n", outcome.Proposals.Accepted, len(outcome.Proposals.Rejected))
	}
	if outcome.CallerGaps != nil {
		fmt.Fprintf(&text, "caller gaps: %d\n", len(*outcome.CallerGaps))
	}
	_, err := io.WriteString(w, text.String())
	return err
}

func Row(m mutant.Mutant) string {
	if m.Bug != "" {
		return fmt.Sprintf("%s:%d %s: %s  [%s]", m.File, m.Line, m.Operator, m.Bug, m.ID)
	}
	original, replacement := shorten(m.Original, m.Replacement)
	return fmt.Sprintf("%s:%d %s: %s -> %s  [%s]", m.File, m.Line, m.Operator, original, replacement, m.ID)
}

func Counts(mutants []mutant.Mutant, base string) string {
	counts := []string{fmt.Sprintf("mutants: %d", len(mutants))}
	for _, status := range []mutant.Status{mutant.Killed, mutant.Lived, mutant.NotCovered, mutant.NotViable, mutant.TimedOut, mutant.InfraError} {
		count := 0
		for _, m := range mutants {
			if m.Verdict.Status == status {
				count++
			}
		}
		if count > 0 {
			counts = append(counts, fmt.Sprintf("%s: %d", strings.ToLower(status.String()), count))
		}
	}
	line := strings.Join(counts, ", ")
	if base != "" {
		line += fmt.Sprintf(" (base %s)", base[:min(len(base), 10)])
	}
	return line
}

func lineRanges(lines []int) string {
	var ranges []string
	for i := 0; i < len(lines); {
		last := i
		for last+1 < len(lines) && lines[last+1] == lines[last]+1 {
			last++
		}
		if last == i {
			ranges = append(ranges, strconv.Itoa(lines[i]))
		} else {
			ranges = append(ranges, fmt.Sprintf("%d-%d", lines[i], lines[last]))
		}
		i = last + 1
	}
	return strings.Join(ranges, ",")
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func shorten(original, replacement string) (string, string) {
	a, b := []rune(strings.Join(strings.Fields(original), " ")), []rune(strings.Join(strings.Fields(replacement), " "))
	same := 0
	for same < len(a) && same < len(b) && a[same] == b[same] {
		same++
	}
	start := 0
	if max(len(a), len(b)) > shortText && same > shortText-shortContext {
		start = same - shortContext
		for start > 0 && a[start-1] != ' ' {
			start--
		}
	}
	return cut(a, start), cut(b, start)
}

func cut(text []rune, start int) string {
	if len(text) == 0 {
		return "(nothing)"
	}
	prefix := ""
	if start > 0 {
		prefix = "... "
	}
	if rest := text[start:]; len(rest) > shortText {
		return prefix + string(rest[:shortText]) + " ..."
	}
	return prefix + string(text[start:])
}
