//go:build unit

package cmd

import (
	"errors"
	"testing"
	"time"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/run"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestRun(t *testing.T) {
	t.Run("exit", func(t *testing.T) {
		withStatuses := func(statuses ...mutant.Status) run.Outcome {
			var outcome run.Outcome
			for _, status := range statuses {
				outcome.Mutants = append(outcome.Mutants, mutant.Mutant{Verdict: mutant.Verdict{Status: status}})
			}
			return outcome
		}
		exitCode := func(t *testing.T, err error) int {
			t.Helper()
			if err == nil {
				return 0
			}
			var exit cli.ExitCoder
			require.True(t, errors.As(err, &exit))
			return exit.ExitCode()
		}

		t.Run("a run where each mutant got a verdict and none survived exits 0, also when no mutant builds", func(t *testing.T) {
			outcome := withStatuses(mutant.Killed, mutant.NotViable, mutant.TimedOut)

			require.Equal(t, 0, exitCode(t, exitOf(outcome, 0)))
		})

		t.Run("a run with an INFRA ERROR and no survivor exits 1, and says how many mutants have no verdict", func(t *testing.T) {
			outcome := withStatuses(mutant.Killed, mutant.InfraError, mutant.InfraError)

			err := exitOf(outcome, 0)

			require.Equal(t, 1, exitCode(t, err))
			require.Equal(t, "mutants has no verdict for 2 of the mutants: run each one again with mutants rerun ID", err.Error())
		})

		t.Run("a survivor wins over a mutant with no verdict, and exits 10", func(t *testing.T) {
			outcome := withStatuses(mutant.InfraError, mutant.Lived)

			require.Equal(t, 10, exitCode(t, exitOf(outcome, 0)))
		})

		t.Run("the limit wins over a survivor, and exits 124", func(t *testing.T) {
			outcome := withStatuses(mutant.Lived)
			outcome.Stopped = true

			require.Equal(t, 124, exitCode(t, exitOf(outcome, time.Minute)))
		})
	})
}
