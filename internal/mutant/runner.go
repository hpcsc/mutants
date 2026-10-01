package mutant

import "context"

// Runner returns an error from Run only when the whole run must stop.
type Runner interface {
	Run(ctx context.Context, m Mutant) (Verdict, error)
}
