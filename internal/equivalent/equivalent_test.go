//go:build unit

package equivalent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/equivalent"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/stretchr/testify/require"
)

func TestList(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		t.Run("reads each entry with its line, also an empty replacement and a number as a text", func(t *testing.T) {
			content := `- id: shop/cart.go:Total:RETURN_EMPTY#2
  original: total
  replacement: "0"
  reason: no caller reads the total of an empty cart
- id: shop/cart.go:Close:STATEMENT_REMOVE#1
  original: close(done)
  replacement:
  reason: the process ends after Close
`

			list, err := equivalent.Read(".mutants/equivalent.yml", []byte(content))

			require.NoError(t, err)
			require.Equal(t, equivalent.List{File: ".mutants/equivalent.yml", Entries: []equivalent.Entry{
				{ID: mutant.ID{File: "shop/cart.go", Function: "Total", Operator: "RETURN_EMPTY", Number: 2}, Original: "total", Replacement: "0", Reason: "no caller reads the total of an empty cart", Line: 1},
				{ID: mutant.ID{File: "shop/cart.go", Function: "Close", Operator: "STATEMENT_REMOVE", Number: 1}, Original: "close(done)", Replacement: "", Reason: "the process ends after Close", Line: 5},
			}}, list)
		})

		t.Run("an empty file holds no entry", func(t *testing.T) {
			list, err := equivalent.Read(".mutants/equivalent.yml", []byte("# no equivalent mutant yet\n"))

			require.NoError(t, err)
			require.Equal(t, equivalent.List{File: ".mutants/equivalent.yml"}, list)
		})

		t.Run("an entry with no reason returns an error that tells why the reason is needed", func(t *testing.T) {
			content := "- id: a.go:f:RETURN_EMPTY#1\n  original: x\n  replacement: \"0\"\n  reason: \" \"\n"

			_, err := equivalent.Read(".mutants/equivalent.yml", []byte(content))

			require.EqualError(t, err, "the entry in .mutants/equivalent.yml (line 1) has no reason: the reason tells a reviewer why no test can kill the mutant")
		})

		t.Run("an entry without one of the keys returns an error that names the key", func(t *testing.T) {
			content := "- id: a.go:f:RETURN_EMPTY#1\n  original: x\n  reason: no caller reads it\n"

			_, err := equivalent.Read(".mutants/equivalent.yml", []byte(content))

			require.EqualError(t, err, "the entry in .mutants/equivalent.yml (line 1) has no replacement")
		})

		t.Run("an unknown key returns an error with its line and the known keys", func(t *testing.T) {
			content := "- id: a.go:f:RETURN_EMPTY#1\n  original: x\n  replacement: \"0\"\n  reason: no caller reads it\n  why: typo\n"

			_, err := equivalent.Read(".mutants/equivalent.yml", []byte(content))

			require.EqualError(t, err, "unknown key why in .mutants/equivalent.yml (line 5): the keys are id, original, replacement, reason")
		})

		t.Run("an id that is not a mutant id returns an error with the line of the entry", func(t *testing.T) {
			content := "- id: RETURN_EMPTY\n  original: x\n  replacement: \"0\"\n  reason: no caller reads it\n"

			_, err := equivalent.Read(".mutants/equivalent.yml", []byte(content))

			require.ErrorContains(t, err, "the entry in .mutants/equivalent.yml (line 1): \"RETURN_EMPTY\" is not a mutant id")
		})

		t.Run("a file that is not a list returns an error that names the keys", func(t *testing.T) {
			_, err := equivalent.Read(".mutants/equivalent.yml", []byte("id: a.go:f:RETURN_EMPTY#1\n"))

			require.EqualError(t, err, ".mutants/equivalent.yml must hold a list of equivalent mutants, each with the keys id, original, replacement, reason")
		})
	})

	t.Run("load", func(t *testing.T) {
		entry := "- id: a.go:f:RETURN_EMPTY#1\n  original: x\n  replacement: \"0\"\n  reason: %s\n"
		write := func(t *testing.T, path, reason string) {
			t.Helper()
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(entry, "%s", reason)), 0o644))
		}

		t.Run("reads the file of the repository, and says on stderr that it ignores the file of the shared git folder", func(t *testing.T) {
			root, shared := t.TempDir(), t.TempDir()
			write(t, filepath.Join(root, ".mutants", "equivalent.yml"), "in the repository")
			write(t, filepath.Join(shared, "mutants", "equivalent.yml"), "in the shared git folder")
			var stderr strings.Builder

			list, err := equivalent.Load(root, shared, &stderr)

			require.NoError(t, err)
			require.Equal(t, ".mutants/equivalent.yml", list.File)
			require.Equal(t, "in the repository", list.Entries[0].Reason)
			require.Equal(t, "mutants ignores "+filepath.Join(shared, "mutants", "equivalent.yml")+", because .mutants/equivalent.yml exists\n", stderr.String())
		})

		t.Run("reads the file of the shared git folder when the repository has none", func(t *testing.T) {
			root := t.TempDir()
			shared := filepath.Join(root, ".git")
			write(t, filepath.Join(shared, "mutants", "equivalent.yml"), "in the shared git folder")

			list, err := equivalent.Load(root, shared, &strings.Builder{})

			require.NoError(t, err)
			require.Equal(t, ".git/mutants/equivalent.yml", list.File)
			require.Equal(t, "in the shared git folder", list.Entries[0].Reason)
		})

		t.Run("a repository with no file has no entry", func(t *testing.T) {
			list, err := equivalent.Load(t.TempDir(), t.TempDir(), &strings.Builder{})

			require.NoError(t, err)
			require.Equal(t, equivalent.List{}, list)
		})
	})

	t.Run("verdict", func(t *testing.T) {
		list := equivalent.List{Entries: []equivalent.Entry{{
			ID:          mutant.ID{File: "shop/cart.go", Function: "Total", Operator: "RETURN_EMPTY", Number: 1},
			Original:    "total",
			Replacement: "0",
			Reason:      "no caller reads the total of an empty cart",
		}}}
		withStatus := func(status mutant.Status, function string, number int, original, replacement string) mutant.Mutant {
			return mutant.Mutant{
				ID:          mutant.ID{File: "shop/cart.go", Function: function, Operator: "RETURN_EMPTY", Number: number},
				Original:    original,
				Replacement: replacement,
				Verdict:     mutant.Verdict{Status: status},
			}
		}
		equivalentVerdict := mutant.Verdict{Status: mutant.Equivalent, Detail: "no caller reads the total of an empty cart"}

		t.Run("makes a LIVED mutant with the same file, function, operator and edit EQUIVALENT, also when the number of its id is another, because a new mutant earlier in the function changes it", func(t *testing.T) {
			require.Equal(t, equivalentVerdict, list.Verdict(withStatus(mutant.Lived, "Total", 3, "total", "0")))
		})

		t.Run("compares the edit with its white space as single spaces", func(t *testing.T) {
			spaced := equivalent.List{Entries: []equivalent.Entry{{ID: list.Entries[0].ID, Original: "a +\n\tb", Replacement: "0", Reason: "no caller reads the total of an empty cart"}}}

			require.Equal(t, equivalentVerdict, spaced.Verdict(withStatus(mutant.Lived, "Total", 1, "a + b", "0")))
		})

		t.Run("keeps the verdict of a mutant that a test kills or that no test runs", func(t *testing.T) {
			verdicts := []mutant.Verdict{
				list.Verdict(withStatus(mutant.Killed, "Total", 1, "total", "0")),
				list.Verdict(withStatus(mutant.NotCovered, "Total", 1, "total", "0")),
			}

			require.Equal(t, []mutant.Verdict{{Status: mutant.Killed}, {Status: mutant.NotCovered}}, verdicts)
		})

		t.Run("keeps LIVED for the same edit in another function", func(t *testing.T) {
			require.Equal(t, mutant.Verdict{Status: mutant.Lived}, list.Verdict(withStatus(mutant.Lived, "Subtotal", 1, "total", "0")))
		})

		t.Run("keeps LIVED for another edit in the same function", func(t *testing.T) {
			require.Equal(t, mutant.Verdict{Status: mutant.Lived}, list.Verdict(withStatus(mutant.Lived, "Total", 1, "total", "nil")))
		})
	})
}

func FuzzEquivalentRead(f *testing.F) {
	for _, seed := range []string{
		"- id: a.go:f:RETURN_EMPTY#1\n  original: x\n  replacement: \"0\"\n  reason: no caller reads it\n",
		"- id: a.go:f:RETURN_EMPTY#1\n  original: x\n  replacement:\n  reason: no caller reads it\n",
		"- [a, b]\n",
		"id: a\n",
		"- id: [a]\n",
		"- id: a.go:f:X#1\n  original: x\n  replacement: y\n  reason: r\n  extra: 1\n",
		"---\n---\n",
		"- &anchor\n  id: a.go:f:X#1\n- *anchor\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, content string) {
		list, err := equivalent.Read("equivalent.yml", []byte(content))
		if err != nil {
			return
		}
		for _, entry := range list.Entries {
			require.NotEmpty(t, entry.Original)
			require.NotEmpty(t, strings.TrimSpace(entry.Reason))
		}
	})
}
