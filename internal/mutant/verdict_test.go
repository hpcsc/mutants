//go:build unit

package mutant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/stretchr/testify/require"
)

func TestVerdict(t *testing.T) {
	t.Run("confirm kill", func(t *testing.T) {
		killed := mutant.Verdict{Status: mutant.Killed, Detail: "--- FAIL: TestMax"}
		again := func(second mutant.Verdict, err error) (func(context.Context) (mutant.Verdict, error), *int) {
			runs := 0
			return func(context.Context) (mutant.Verdict, error) {
				runs++
				return second, err
			}, &runs
		}

		t.Run("a kill that the second run repeats stays KILLED, with the detail of the first run", func(t *testing.T) {
			second, _ := again(mutant.Verdict{Status: mutant.Killed, Detail: "--- FAIL: TestMin"}, nil)

			verdict, err := mutant.ConfirmKill(context.Background(), killed, second)

			require.NoError(t, err)
			require.Equal(t, killed, verdict)
		})

		t.Run("a kill whose tests pass in the second run is UNSTABLE, with the failure of the first run", func(t *testing.T) {
			second, _ := again(mutant.Verdict{Status: mutant.Lived}, nil)

			verdict, err := mutant.ConfirmKill(context.Background(), killed, second)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Unstable, Detail: "a test failed, and passed when the tests ran again: --- FAIL: TestMax"}, verdict)
		})

		t.Run("a kill whose second run times out stays KILLED", func(t *testing.T) {
			second, _ := again(mutant.Verdict{Status: mutant.TimedOut}, nil)

			verdict, err := mutant.ConfirmKill(context.Background(), killed, second)

			require.NoError(t, err)
			require.Equal(t, killed, verdict)
		})

		t.Run("a verdict other than a kill does not run the tests again", func(t *testing.T) {
			second, runs := again(mutant.Verdict{Status: mutant.Killed}, nil)

			verdict, err := mutant.ConfirmKill(context.Background(), mutant.Verdict{Status: mutant.Lived}, second)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Lived}, verdict)
			require.Equal(t, 0, *runs)
		})

		t.Run("an error of the second run stops the run", func(t *testing.T) {
			stopped := errors.New("the run was interrupted")
			second, _ := again(mutant.Verdict{}, stopped)

			_, err := mutant.ConfirmKill(context.Background(), killed, second)

			require.ErrorIs(t, err, stopped)
		})
	})
}
