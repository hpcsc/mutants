//go:build unit

package golang_test

import (
	"context"
	"testing"

	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/stretchr/testify/require"
)

func TestTagsOfTests(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		t.Run("counts each tag that a test file needs, across the files", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"a/a_test.go": "//go:build unit\n\npackage a\n",
				"b/b_test.go": "// A comment before the constraint.\n\n//go:build unit\n\npackage b\n",
				"c/c_test.go": "//go:build integration || e2e\n\npackage c\n",
			})

			tags, err := golang.TagsOfTests(context.Background(), root)

			require.NoError(t, err)
			require.Equal(t, map[string]int{"unit": 2, "integration": 1, "e2e": 1}, tags)
		})

		t.Run("leaves out a tag that a file only excludes, and the tags that go sets itself", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"a/a_test.go": "//go:build unit && !integration\n\npackage a\n",
				"d/d_test.go": "//go:build linux && amd64\n\npackage d\n",
				"e/e_test.go": "//go:build go1.21 && cgo && !windows\n\npackage e\n",
			})

			tags, err := golang.TagsOfTests(context.Background(), root)

			require.NoError(t, err)
			require.Equal(t, map[string]int{"unit": 1}, tags)
		})

		t.Run("skips a file that is not a test, a constraint after the package clause, and the testdata, hidden and vendor folders", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"f/f.go":             "//go:build tools\n\npackage f\n",
				"g/g_test.go":        "package g\n\n//go:build late\n",
				"testdata/t_test.go": "//go:build intestdata\n\npackage t\n",
				".cache/c_test.go":   "//go:build inhidden\n\npackage c\n",
				"vendor/v/v_test.go": "//go:build invendor\n\npackage v\n",
			})

			tags, err := golang.TagsOfTests(context.Background(), root)

			require.NoError(t, err)
			require.Empty(t, tags)
		})
	})
}
