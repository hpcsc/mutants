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
	Keep(candidate operator.Edit) bool
	Function(file string, offset int) string
	// Uncovered gives the mutants that no test runs. The value is the detail of the verdict: empty, or a text
	// that holds for each mutant of one package.
	Uncovered(ctx context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error)
	CallerGaps(ctx context.Context, changed diff.Lines) ([]CallerGap, error)
	Runner() mutant.Runner
}

// CallerGap holds the changed lines of one function that the tests of its own package run, but that no test
// of a changed package that calls the function runs. Callers are the folders of those packages.
type CallerGap struct {
	File     string
	Function string
	Lines    []int
	Callers  []string
}
