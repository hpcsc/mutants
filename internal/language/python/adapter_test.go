//go:build unit

package python_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/language/python"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/stretchr/testify/require"
)

const pyproject = "[tool.pytest.ini_options]\npythonpath = [\".\"]\n"

const discount = "LIMIT = 100\n\n\ndef discount(total):\n    if total >= LIMIT:\n        return 10\n    return 0\n\n\n" +
	"def unused(total):\n    return total + 1\n"

const discountTest = "from shop.discount import discount\n\n\ndef test_low():\n    assert discount(50) == 0\n\n\n" +
	"def test_edge():\n    assert discount(100) == 10\n"

func newProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return root
}

func shop(extra map[string]string) map[string]string {
	files := map[string]string{
		"pyproject.toml":         pyproject,
		"shop/__init__.py":       "",
		"shop/discount.py":       discount,
		"tests/test_discount.py": discountTest,
	}
	for name, content := range extra {
		files[name] = content
	}
	return files
}

func newAdapter(root string) language.Adapter {
	return python.New(root, python.Settings{Command: []string{withCoverage}, Workers: 2})
}

func editOf(t *testing.T, root, file, rule, original, replacement string) operator.Edit {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, file))
	require.NoError(t, err)
	start := strings.Index(string(source), original)
	require.GreaterOrEqual(t, start, 0, "%q is not in %s", original, file)
	operatorName, _, _ := strings.Cut(rule, "/")
	return operator.Edit{File: file, Operator: operatorName, Rule: rule, Start: start, End: start + len(original), Original: original, Replacement: replacement}
}

func editIn(t *testing.T, root, file, rule, context, original, replacement string) operator.Edit {
	t.Helper()
	edit := editOf(t, root, file, rule, context, "")
	edit.Start += strings.Index(context, original)
	edit.End, edit.Original, edit.Replacement = edit.Start+len(original), original, replacement
	return edit
}

func mutantOf(t *testing.T, root, file, original, replacement string) mutant.Mutant {
	t.Helper()
	edit := editOf(t, root, file, "TEST", original, replacement)
	source, err := os.ReadFile(filepath.Join(root, file))
	require.NoError(t, err)
	before := string(source[:edit.Start])
	return mutant.Mutant{
		ID:          mutant.ID{File: file, Function: "f", Operator: "TEST", Number: edit.Start},
		File:        file,
		Language:    "python",
		Line:        strings.Count(before, "\n") + 1,
		Column:      edit.Start - strings.LastIndex(before, "\n"),
		Start:       edit.Start,
		End:         edit.End,
		Operator:    "TEST",
		Original:    original,
		Replacement: replacement,
	}
}

func TestAdapter(t *testing.T) {
	t.Run("keep", func(t *testing.T) {
		t.Run("drops an edit whose replacement is the same as the original", func(t *testing.T) {
			root := newProject(t, map[string]string{"a.py": "def f():\n    return None\n"})

			require.False(t, newAdapter(root).Keep(editOf(t, root, "a.py", "RETURN_EMPTY/none", "None", "None")))
		})

		t.Run("keeps the RETURN_EMPTY value that fits the return type of the function", func(t *testing.T) {
			source := "def count() -> int:\n    return n\n\n\ndef name() -> str:\n    return s\n\n\ndef ok() -> bool:\n    return b\n\n\n" +
				"def items() -> list[int]:\n    return xs\n\n\ndef table() -> typing.Dict[str, int]:\n    return d\n\n\n" +
				"def maybe() -> int | None:\n    return m\n\n\ndef cart() -> Cart:\n    return c\n\n\ndef plain():\n    return p\n"
			root := newProject(t, map[string]string{"a.py": source})
			adapter := newAdapter(root)
			kept := map[string]string{}
			for _, original := range []string{"n", "s", "b", "xs", "d", "m", "c", "p"} {
				for _, empty := range []string{"None", "0", `""`, "False", "[]", "{}"} {
					if adapter.Keep(editIn(t, root, "a.py", "RETURN_EMPTY/x", "return "+original+"\n", original, empty)) {
						kept[original] = empty
					}
				}
			}

			require.Equal(t, map[string]string{"n": "0", "s": `""`, "b": "False", "xs": "[]", "d": "{}", "m": "None", "c": "None", "p": "None"}, kept)
		})

		t.Run("keeps a RETURN_TRUE edit of a value that is not a condition only in a function that returns bool", func(t *testing.T) {
			root := newProject(t, map[string]string{"a.py": "def ok() -> bool:\n    return check(a)\n\n\ndef count() -> int:\n    return size(a)\n"})
			adapter := newAdapter(root)

			require.True(t, adapter.Keep(editOf(t, root, "a.py", "RETURN_TRUE/annotated", "check(a)", "True")))
			require.False(t, adapter.Keep(editOf(t, root, "a.py", "RETURN_TRUE/annotated", "size(a)", "True")))
		})

		t.Run("keeps a STATEMENT_REMOVE of an assignment only when each name that it assigns has a value before it in its function", func(t *testing.T) {
			source := `def sweep(self, items, dry_run, counts):
    total = 0
    for item in items:
        total = total + item
        counts[item] = total
        self.total = total
    first = items[0]
    if not dry_run:
        dry_run = True
    with open("log") as handle:
        handle = None
    a, b = 1, 2

    def inner(c):
        dry_run = c
        nonlocal total
        total = c


def limit():
    global LIMIT
    LIMIT = 2
`
			root := newProject(t, map[string]string{"a.py": source})
			adapter := newAdapter(root)
			kept := map[string]bool{}
			for _, assignment := range []string{
				"total = 0", "total = total + item", "counts[item] = total", "self.total = total", "first = items[0]",
				"dry_run = True", "handle = None", "a, b = 1, 2", "dry_run = c", "total = c", "LIMIT = 2",
			} {
				_, value, _ := strings.Cut(assignment, " = ")
				kept[assignment] = adapter.Keep(editOf(t, root, "a.py", "STATEMENT_REMOVE/assign", assignment, "_ = "+value))
			}

			require.Equal(t, map[string]bool{
				"total = 0": false, "total = total + item": true, "counts[item] = total": true, "self.total = total": true,
				"first = items[0]": false, "dry_run = True": true, "handle = None": true, "a, b = 1, 2": false,
				"dry_run = c": false, "total = c": true, "LIMIT = 2": true,
			}, kept)
		})
	})

	t.Run("function", func(t *testing.T) {
		t.Run("names the classes and the functions that hold the offset, from the outside in", func(t *testing.T) {
			source := "LIMIT = 1\n\n\nclass Cart:\n    async def total(self):\n        def add(a, b):\n            return a + b\n        return add(1, 2)\n"
			root := newProject(t, map[string]string{"a.py": source})
			adapter := newAdapter(root)

			require.Equal(t, []string{"", "Cart.total.add", "Cart.total"}, []string{
				adapter.Function("a.py", strings.Index(source, "1")),
				adapter.Function("a.py", strings.Index(source, "a + b")),
				adapter.Function("a.py", strings.Index(source, "add(1, 2)")),
			})
		})
	})

	t.Run("uncovered", func(t *testing.T) {
		t.Run("a mutant on a line that no test runs is NOT COVERED, and a mutant on a line that a test runs is not", func(t *testing.T) {
			root := newProject(t, shop(nil))
			unused := mutantOf(t, root, "shop/discount.py", "total + 1", "total - 1")
			used := mutantOf(t, root, "shop/discount.py", "total >= LIMIT", "total > LIMIT")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{unused, used})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{unused.ID: ""}, uncovered)
		})

		t.Run("a mutant of a file that no test imports is NOT COVERED, with the file in the detail", func(t *testing.T) {
			root := newProject(t, shop(map[string]string{"shop/report.py": "def total(xs):\n    return sum(xs) + 1\n"}))
			m := mutantOf(t, root, "shop/report.py", "sum(xs) + 1", "sum(xs) - 1")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{m.ID: "no test imports shop/report.py"}, uncovered)
		})

		t.Run("a mutant of a project with no tests is NOT COVERED, with the project in the detail", func(t *testing.T) {
			root := newProject(t, map[string]string{"services/api/pyproject.toml": pyproject, "services/api/app.py": "LIMIT = 1\n"})
			m := mutantOf(t, root, "services/api/app.py", "1", "2")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{m.ID: "the Python project in services/api has no tests"}, uncovered)
		})

		t.Run("tests that fail with the real code return an error that names the project of the file", func(t *testing.T) {
			root := newProject(t, map[string]string{
				"services/api/pyproject.toml":    pyproject,
				"services/api/app.py":            "def one():\n    return 1\n",
				"services/api/tests/test_app.py": "from app import one\n\n\ndef test_one():\n    assert one() == 2\n",
			})
			m := mutantOf(t, root, "services/api/app.py", "1", "2")

			_, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m})

			require.ErrorContains(t, err, "the tests of the Python project in services/api fail with the real code")
		})

		t.Run("a project without coverage.py returns an error that tells how to add it", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "shop/discount.py", "total >= LIMIT", "total > LIMIT")
			adapter := python.New(root, python.Settings{Command: []string{withoutCoverage}, Workers: 2})

			_, err := adapter.Uncovered(context.Background(), []mutant.Mutant{m})

			require.EqualError(t, err, "the Python project in . has no coverage.py: add coverage or pytest-cov to its dev dependencies")
		})
	})

	t.Run("run", func(t *testing.T) {
		t.Run("a mutant that a test catches is KILLED, with the failed test as the detail", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "shop/discount.py", "total >= LIMIT", "total > LIMIT")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Killed, Detail: "FAILED tests/test_discount.py::test_edge - assert 0 == 10"}, verdict)
		})

		t.Run("a mutant that each test passes LIVED", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "shop/discount.py", "return 0", "return 0 * 2")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Lived}, verdict)
		})

		t.Run("a mutant runs only the tests that run its line", func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "other.log")
			t.Setenv("MUTANTS_TEST_LOG", log)
			other := "import os\n\n\ndef test_other():\n    with open(os.environ[\"MUTANTS_TEST_LOG\"], \"a\") as log:\n        log.write(\"ran\\n\")\n"
			root := newProject(t, shop(map[string]string{"tests/test_other.py": other}))
			m := mutantOf(t, root, "shop/discount.py", "total >= LIMIT", "total > LIMIT")

			_, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			content, err := os.ReadFile(log)
			require.NoError(t, err)
			require.Equal(t, "ran\n", string(content), "only the coverage run runs the other test")
		})

		t.Run("a mutant that does not compile is NOT VIABLE", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "shop/discount.py", "return 10", "return 10 +")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.NotViable, verdict.Status)
			require.Contains(t, verdict.Detail, "the mutant does not compile")
		})

		t.Run("a mutant that breaks the import of its module is KILLED", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "shop/discount.py", "LIMIT = 100", "LIMIT = 100 / 0")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Killed, verdict.Status)
			require.Contains(t, verdict.Detail, "ZeroDivisionError")
		})

		t.Run("a mutant that breaks the import of a module that conftest.py imports is KILLED", func(t *testing.T) {
			root := newProject(t, shop(map[string]string{"tests/conftest.py": "import shop.discount\n"}))
			m := mutantOf(t, root, "shop/discount.py", "LIMIT = 100", "LIMIT = 100 / 0")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Killed, verdict.Status)
			require.Contains(t, verdict.Detail, "ImportError while loading conftest")
			require.Contains(t, verdict.Detail, "ZeroDivisionError")
		})

		t.Run("the module of the mutant loads from the mutant also in a process that a test starts", func(t *testing.T) {
			child := "import subprocess\nimport sys\n\n\ndef test_child():\n" +
				"    output = subprocess.run([sys.executable, \"-c\", \"from shop.discount import discount; print(discount(100))\"], capture_output=True, text=True)\n" +
				"    assert output.stdout.strip() == \"10\"\n"
			root := newProject(t, shop(map[string]string{"tests/test_discount.py": child}))
			m := mutantOf(t, root, "shop/discount.py", "total >= LIMIT", "total > LIMIT")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Killed, verdict.Status)
		})

		t.Run("writes no file into the project", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "shop/discount.py", "total >= LIMIT", "total > LIMIT")
			before := filesIn(t, root)

			_, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, before, filesIn(t, root))
		})
	})
}

func filesIn(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		files = append(files, path)
		return err
	}))
	return files
}
