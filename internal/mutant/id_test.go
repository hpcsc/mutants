//go:build unit

package mutant_test

import (
	"testing"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/stretchr/testify/require"
)

func TestID(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		t.Run("names the file, the function, the operator and the number", func(t *testing.T) {
			id := mutant.ID{File: "internal/order/handler.go", Function: "(*Handler).accounts", Operator: "BRANCH_IF", Number: 1}

			require.Equal(t, "internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1", id.String())
		})
	})

	t.Run("parse", func(t *testing.T) {
		t.Run("reads an id back into its parts", func(t *testing.T) {
			id, err := mutant.ParseID("internal/order/handler.go:(*Handler).accounts:BRANCH_IF#12")

			require.NoError(t, err)
			require.Equal(t, mutant.ID{File: "internal/order/handler.go", Function: "(*Handler).accounts", Operator: "BRANCH_IF", Number: 12}, id)
		})

		t.Run("a file name with a colon keeps its colon", func(t *testing.T) {
			id, err := mutant.ParseID("a:b.go:f:ARITHMETIC_BASE#2")

			require.NoError(t, err)
			require.Equal(t, mutant.ID{File: "a:b.go", Function: "f", Operator: "ARITHMETIC_BASE", Number: 2}, id)
		})

		t.Run("reads a number that no mutant has, such as 0, so that the lookup says that no mutant has the id", func(t *testing.T) {
			id, err := mutant.ParseID("a.go:f:PROPOSED#000000")

			require.NoError(t, err)
			require.Equal(t, mutant.ID{File: "a.go", Function: "f", Operator: "PROPOSED", Number: 0}, id)
		})

		t.Run("text that is not an id returns an error that shows the form of an id", func(t *testing.T) {
			for _, text := range []string{"", "handler.go:42", "handler.go:f:BRANCH_IF", "handler.go:f:BRANCH_IF#-1", "handler.go:f:BRANCH_IF#+1", "handler.go:f:#1", "f:BRANCH_IF#1", ":f:BRANCH_IF#1"} {
				_, err := mutant.ParseID(text)

				require.ErrorContains(t, err, "<file>:<function>:<operator>#<number>", text)
			}
		})
	})
}

func TestCounter(t *testing.T) {
	t.Run("next", func(t *testing.T) {
		t.Run("counts each operator in each function from 1", func(t *testing.T) {
			var counter mutant.Counter

			ids := []string{
				counter.Next("a.go", "f", "BRANCH_IF").String(),
				counter.Next("a.go", "f", "BRANCH_IF").String(),
				counter.Next("a.go", "f", "RETURN_EMPTY").String(),
				counter.Next("a.go", "g", "BRANCH_IF").String(),
				counter.Next("b.go", "f", "BRANCH_IF").String(),
			}

			require.Equal(t, []string{"a.go:f:BRANCH_IF#1", "a.go:f:BRANCH_IF#2", "a.go:f:RETURN_EMPTY#1", "a.go:g:BRANCH_IF#1", "b.go:f:BRANCH_IF#1"}, ids)
		})
	})
}

func FuzzIDParse(f *testing.F) {
	for _, seed := range []string{
		"internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1",
		"a:b.go:f:ARITHMETIC_BASE#2",
		"a.go:f:PROPOSED#000000",
		"a.go::RETURN_EMPTY#3",
		"a.go:f#g:BRANCH_IF#4",
		"handler.go:f:BRANCH_IF#-1",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		id, err := mutant.ParseID(text)
		if err != nil {
			return
		}

		again, err := mutant.ParseID(id.String())

		require.NoError(t, err)
		require.Equal(t, id, again)
	})
}
