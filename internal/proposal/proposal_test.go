//go:build unit

package proposal_test

import (
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/proposal"
	"github.com/stretchr/testify/require"
)

func TestProposal(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		t.Run("reads one proposal from each line, and skips a blank line", func(t *testing.T) {
			text := `{"file": "a.go", "old": "x > 0", "new": "x >= 0", "bug": "zero counts as positive"}

{"id": "M2", "file": "b.go", "old": "close(done)\n", "new": "", "bug": "the workers never stop"}
`

			proposals, err := proposal.Read(strings.NewReader(text))

			require.NoError(t, err)
			require.Equal(t, []proposal.Proposal{
				{File: "a.go", Old: "x > 0", New: "x >= 0", Bug: "zero counts as positive"},
				{File: "b.go", Old: "close(done)\n", New: "", Bug: "the workers never stop"},
			}, proposals)
		})

		t.Run("reads the last line also without a line end", func(t *testing.T) {
			proposals, err := proposal.Read(strings.NewReader(`{"file": "a.go", "old": "a", "new": "b", "bug": "c"}`))

			require.NoError(t, err)
			require.Equal(t, []proposal.Proposal{{File: "a.go", Old: "a", New: "b", Bug: "c"}}, proposals)
		})

		t.Run("a line that is not a JSON object returns an error that names the line", func(t *testing.T) {
			_, err := proposal.Read(strings.NewReader("{\"file\": \"a.go\", \"old\": \"a\", \"new\": \"b\", \"bug\": \"c\"}\nnot json\n"))

			require.ErrorContains(t, err, "line 2: a proposal is one JSON object")
		})

		t.Run("a proposal without a field returns an error that names the field", func(t *testing.T) {
			_, err := proposal.Read(strings.NewReader(`{"file": "a.go", "old": "a", "bug": "c"}`))

			require.EqualError(t, err, `line 1: the proposal has no "new"`)
		})

		t.Run("an empty old returns an error", func(t *testing.T) {
			_, err := proposal.Read(strings.NewReader(`{"file": "a.go", "old": "", "new": "b", "bug": "c"}`))

			require.EqualError(t, err, `line 1: "old" is empty`)
		})

		t.Run("a file with no proposal returns an error", func(t *testing.T) {
			_, err := proposal.Read(strings.NewReader("\n\n"))

			require.EqualError(t, err, "the file holds no proposal")
		})
	})

	t.Run("number", func(t *testing.T) {
		t.Run("depends only on the old and the new text, and has six digits", func(t *testing.T) {
			first := proposal.Proposal{File: "a.go", Old: "x > 0", New: "x >= 0", Bug: "one bug"}
			again := proposal.Proposal{File: "b.go", Old: "x > 0", New: "x >= 0", Bug: "the same edit, in other words"}
			other := proposal.Proposal{File: "a.go", Old: "x > 0", New: "x < 0", Bug: "one bug"}

			require.Equal(t, first.Number(), again.Number())
			require.NotEqual(t, first.Number(), other.Number())
			for _, p := range []proposal.Proposal{first, other} {
				require.GreaterOrEqual(t, p.Number(), 100000)
				require.Less(t, p.Number(), 1000000)
			}
		})
	})
}

func TestStore(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		t.Run("finds a saved proposal by its id", func(t *testing.T) {
			store := proposal.NewStore(t.TempDir())
			saved := proposal.Proposal{File: "a.go", Old: "x > 0", New: "x >= 0", Bug: "zero counts as positive"}

			require.NoError(t, store.Save(map[string]proposal.Proposal{"a.go:f:PROPOSED#123456": saved}))

			found, ok, err := store.Find("a.go:f:PROPOSED#123456")
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, saved, found)
		})

		t.Run("keeps the proposals of an earlier save, and replaces the one with the same id", func(t *testing.T) {
			store := proposal.NewStore(t.TempDir())
			first := proposal.Proposal{File: "a.go", Old: "a", New: "b", Bug: "first"}
			second := proposal.Proposal{File: "b.go", Old: "c", New: "d", Bug: "second"}
			replaced := proposal.Proposal{File: "a.go", Old: "a", New: "b", Bug: "first, in other words"}
			require.NoError(t, store.Save(map[string]proposal.Proposal{"a.go:f:PROPOSED#111111": first}))

			require.NoError(t, store.Save(map[string]proposal.Proposal{"b.go:g:PROPOSED#222222": second, "a.go:f:PROPOSED#111111": replaced}))

			for id, want := range map[string]proposal.Proposal{"a.go:f:PROPOSED#111111": replaced, "b.go:g:PROPOSED#222222": second} {
				found, ok, err := store.Find(id)
				require.NoError(t, err)
				require.True(t, ok, id)
				require.Equal(t, want, found)
			}
		})
	})

	t.Run("find", func(t *testing.T) {
		t.Run("an id that the store does not hold is not found, also before the first save", func(t *testing.T) {
			store := proposal.NewStore(t.TempDir())

			_, ok, err := store.Find("a.go:f:PROPOSED#123456")

			require.NoError(t, err)
			require.False(t, ok)
		})
	})
}
