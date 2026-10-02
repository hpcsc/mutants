//go:build unit

package golang_test

import (
	"context"
	"testing"

	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/stretchr/testify/require"
)

func TestTagsOfTests(t *testing.T) {
	t.Run("tags of tests", func(t *testing.T) {
		t.Run("counts the test files that need each tag, and leaves out excluded tags, tags of go, and other files", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"a/a_test.go":        "//go:build unit\n\npackage a\n",
				"b/b_test.go":        "// A comment before the constraint.\n\n//go:build unit && !integration\n\npackage b\n",
				"c/c_test.go":        "//go:build integration || e2e\n\npackage c\n",
				"d/d_test.go":        "//go:build linux && amd64\n\npackage d\n",
				"e/e_test.go":        "//go:build go1.21 && cgo && !windows\n\npackage e\n",
				"f/f.go":             "//go:build tools\n\npackage f\n",
				"g/g_test.go":        "package g\n\n//go:build late\n",
				"testdata/t_test.go": "//go:build hidden\n\npackage t\n",
				".cache/c_test.go":   "//go:build hidden\n\npackage c\n",
				"vendor/v/v_test.go": "//go:build hidden\n\npackage v\n",
			})

			tags, err := golang.TagsOfTests(context.Background(), root)

			require.NoError(t, err)
			require.Equal(t, map[string]int{"unit": 2, "integration": 1, "e2e": 1}, tags)
		})
	})
}
