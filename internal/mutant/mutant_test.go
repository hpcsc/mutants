//go:build unit

package mutant_test

import (
	"testing"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/stretchr/testify/require"
)

func TestMutant(t *testing.T) {
	t.Run("apply", func(t *testing.T) {
		t.Run("replaces the original text with the replacement", func(t *testing.T) {
			m := mutant.Mutant{File: "a.go", Start: 7, End: 12, Original: "a < b", Replacement: "a <= b"}

			mutated, err := m.Apply([]byte("return a < b\n"))

			require.NoError(t, err)
			require.Equal(t, "return a <= b\n", mutated)
		})

		t.Run("a source that no longer holds the original text gives an error that names the file", func(t *testing.T) {
			m := mutant.Mutant{File: "a.go", Start: 7, End: 12, Original: "a < b", Replacement: "a <= b"}

			_, err := m.Apply([]byte("return a > b\n"))

			require.ErrorContains(t, err, "a.go changed after mutants read it")
		})

		t.Run("a source that ends before the original text gives an error", func(t *testing.T) {
			m := mutant.Mutant{File: "a.go", Start: 7, End: 12, Original: "a < b", Replacement: "a <= b"}

			_, err := m.Apply([]byte("return"))

			require.ErrorContains(t, err, "a.go changed after mutants read it")
		})
	})
}
