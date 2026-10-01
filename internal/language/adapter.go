package language

import (
	"context"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
)

// Adapter takes each file as a path from the root of the repository.
type Adapter interface {
	Name() string
	Extensions() []string
	Keep(candidate operator.Edit) bool
	Function(file string, offset int) string
	// Uncovered gives the mutants that no test runs. The value is empty, or a reason that holds for each
	// mutant of one package.
	Uncovered(ctx context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error)
	Runner() mutant.Runner
}
