package language

import (
	"context"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
)

// Adapter takes each file as a path from the root of the repository.
type Adapter interface {
	Name() string
	Extensions() []string
	IndentationMatters() bool
	Keep(candidate operator.Edit) bool
	Function(file string, offset int) string
	// Uncovered maps each mutant that no test runs to its detail: empty, or a text that holds for each mutant of
	// one package.
	Uncovered(ctx context.Context, mutants []mutant.Mutant, changed diff.Lines) (map[mutant.ID]string, error)
	// Runner tests a mutant with the tests that Uncovered found to run it, so Uncovered must see the mutant first.
	Runner() mutant.Runner
}

// An adapter must not implement CallerGapFinder without the check, because a run reads an empty result as no gap.
type CallerGapFinder interface {
	CallerGaps(ctx context.Context, changed diff.Lines) ([]CallerGap, error)
}

type CallerGap struct {
	File     string
	Function string
	Lines    []int
	Callers  []string
}
