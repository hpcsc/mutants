package mutant

import "context"

type Verdict struct {
	Status Status
	Detail string
}

// ConfirmKill runs the tests again after a kill, because a test can fail for a reason other than the mutant, and a
// false kill hides a gap in the tests.
func ConfirmKill(ctx context.Context, first Verdict, again func(context.Context) (Verdict, error)) (Verdict, error) {
	if first.Status != Killed {
		return first, nil
	}
	second, err := again(ctx)
	if err != nil || second.Status != Lived {
		return first, err
	}
	return Verdict{Status: Unstable, Detail: "a test failed, and passed when the tests ran again: " + first.Detail}, nil
}
