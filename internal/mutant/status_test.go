//go:build unit

package mutant_test

import (
	"testing"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/stretchr/testify/require"
)

func TestStatus(t *testing.T) {
	t.Run("is survivor", func(t *testing.T) {
		t.Run("only LIVED and NOT COVERED count as survivors", func(t *testing.T) {
			survivors := map[mutant.Status]bool{
				mutant.Killed:     false,
				mutant.Lived:      true,
				mutant.NotCovered: true,
				mutant.NotViable:  false,
				mutant.TimedOut:   false,
				mutant.InfraError: false,
				mutant.Unstable:   false,
				mutant.Equivalent: false,
			}
			for status, survivor := range survivors {
				require.Equal(t, survivor, status.IsSurvivor(), status.String())
			}
		})
	})

	t.Run("needs rerun", func(t *testing.T) {
		t.Run("only INFRA ERROR and UNSTABLE have no verdict that a run of the same mutant can change", func(t *testing.T) {
			rerun := map[mutant.Status]bool{}
			for _, status := range []mutant.Status{mutant.Killed, mutant.Lived, mutant.NotCovered, mutant.NotViable, mutant.TimedOut, mutant.InfraError, mutant.Unstable, mutant.Equivalent} {
				rerun[status] = status.NeedsRerun()
			}

			require.Equal(t, map[mutant.Status]bool{
				mutant.Killed:     false,
				mutant.Lived:      false,
				mutant.NotCovered: false,
				mutant.NotViable:  false,
				mutant.TimedOut:   false,
				mutant.InfraError: true,
				mutant.Unstable:   true,
				mutant.Equivalent: false,
			}, rerun)
		})
	})
}
