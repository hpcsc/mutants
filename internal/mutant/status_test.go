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
			}
			for status, survivor := range survivors {
				require.Equal(t, survivor, status.IsSurvivor(), status.String())
			}
		})
	})
}
