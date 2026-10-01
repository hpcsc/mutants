package report

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/hpcsc/mutants/internal/mutant"
)

const (
	shortText    = 40
	shortContext = 12
)

func Rows(w io.Writer, mutants []mutant.Mutant, base string) error {
	sorted := slices.SortedFunc(slices.Values(mutants), func(a, b mutant.Mutant) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	reasons := map[string]int{}
	for _, m := range mutants {
		if m.Result.Status == mutant.NotCovered && m.Result.Detail != "" {
			reasons[m.Result.Detail]++
		}
	}
	printed := map[string]bool{}
	var text strings.Builder
	for _, status := range []mutant.Status{mutant.Lived, mutant.NotCovered, mutant.TimedOut, mutant.InfraError} {
		header := false
		for _, m := range sorted {
			if m.Result.Status != status {
				continue
			}
			if !header {
				fmt.Fprintf(&text, "%s:\n", status)
				header = true
			}
			reason := m.Result.Detail
			switch {
			case status != mutant.NotCovered || reason == "":
				fmt.Fprintf(&text, "  %s\n", Row(m))
			case !printed[reason]:
				fmt.Fprintf(&text, "  %s: %s\n", reason, plural(reasons[reason], "mutant"))
				printed[reason] = true
			}
		}
	}
	text.WriteString(Counts(mutants, base) + "\n")
	_, err := io.WriteString(w, text.String())
	return err
}

func Row(m mutant.Mutant) string {
	original, replacement := shorten(m.Original, m.Replacement)
	return fmt.Sprintf("%s:%d %s: %s -> %s  [%s]", m.File, m.Line, m.Operator, original, replacement, m.ID)
}

func Counts(mutants []mutant.Mutant, base string) string {
	counts := []string{fmt.Sprintf("mutants: %d", len(mutants))}
	for _, status := range []mutant.Status{mutant.Killed, mutant.Lived, mutant.NotCovered, mutant.NotViable, mutant.TimedOut, mutant.InfraError} {
		count := 0
		for _, m := range mutants {
			if m.Result.Status == status {
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
