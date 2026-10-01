//go:build unit

package operator_test

import (
	"context"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/operator/astgrep"
	"github.com/stretchr/testify/require"
)

func loadPack(t *testing.T, repository string) operator.Pack {
	t.Helper()
	pack, err := operator.Load("go", repository)
	require.NoError(t, err)
	return pack
}

func operatorsOf(pack operator.Pack) []string {
	var operators []string
	for _, rule := range pack.Rules() {
		if !slices.Contains(operators, rule.Operator) {
			operators = append(operators, rule.Operator)
		}
	}
	return operators
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func editsOf(t *testing.T, operatorName, source string) []string {
	t.Helper()
	var changes []string
	for _, edit := range findEdits(t, operatorName, source) {
		changes = append(changes, edit.Original+" -> "+edit.Replacement)
	}
	return changes
}

func findEdits(t *testing.T, operatorName, source string) []operator.Edit {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), source)
	pack, err := loadPack(t, root).Select([]string{operatorName})
	require.NoError(t, err)

	edits, err := pack.Edits(context.Background(), astgrep.New(root), root, []string{"a.go"})

	require.NoError(t, err)
	slices.SortFunc(edits, func(a, b operator.Edit) int { return a.Start - b.Start })
	return edits
}

func mutated(t *testing.T, source string, edit operator.Edit) string {
	t.Helper()
	return formatted(t, source[:edit.Start]+edit.Replacement+source[edit.End:])
}

func formatted(t *testing.T, source string) string {
	t.Helper()
	result, err := format.Source([]byte(source))
	require.NoError(t, err)
	var lines []string
	for _, line := range strings.Split(string(result), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func TestPack(t *testing.T) {
	t.Run("load", func(t *testing.T) {
		t.Run("a rule in the repository adds an operator", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/NIL_MAP.yml"), "id: NIL_MAP\nlanguage: go\nrule:\n  pattern: map[$K]$V{}\nfix: nil\n")

			pack := loadPack(t, repository)

			require.Contains(t, operatorsOf(pack), "NIL_MAP")
			require.Contains(t, operatorsOf(pack), "CONDITIONALS_BOUNDARY")
		})

		t.Run("a rule in the repository replaces the standard rule with the same id", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/plus.yml"), "id: ARITHMETIC_BASE/plus\nlanguage: go\nrule:\n  pattern: $A + $B\nfix: $A * $B\n")
			writeFile(t, filepath.Join(repository, "a.go"), "package a\n\nvar x = 1 + 2\n")
			pack, err := loadPack(t, repository).Select([]string{"ARITHMETIC_BASE"})
			require.NoError(t, err)

			edits, err := pack.Edits(context.Background(), astgrep.New(repository), repository, []string{"a.go"})

			require.NoError(t, err)
			require.Len(t, edits, 1)
			require.Equal(t, "1 * 2", edits[0].Replacement)
		})

		t.Run("a rule without a fix returns an error that names it", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/bad.yml"), "id: NO_FIX\nlanguage: go\nrule:\n  pattern: $A + $B\n")

			_, err := operator.Load("go", repository)

			require.ErrorContains(t, err, "NO_FIX has no fix")
		})

		t.Run("a rule for another language returns an error that names it", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/ts.yml"), "id: TS\nlanguage: typescript\nrule:\n  pattern: $A + $B\nfix: $A - $B\n")

			_, err := operator.Load("go", repository)

			require.ErrorContains(t, err, "TS is for \"typescript\"")
		})

		t.Run("a language with no operators returns an error", func(t *testing.T) {
			_, err := operator.Load("cobol", t.TempDir())

			require.ErrorContains(t, err, "no operators for cobol")
		})
	})

	t.Run("select", func(t *testing.T) {
		t.Run("no name runs every operator except the ones that are off by default", func(t *testing.T) {
			pack, err := loadPack(t, t.TempDir()).Select(nil)

			require.NoError(t, err)
			require.Contains(t, operatorsOf(pack), "CONDITIONALS_BOUNDARY")
			require.Contains(t, operatorsOf(pack), "SWAP_FIELDS")
			require.NotContains(t, operatorsOf(pack), "ERRORF_WRAP")
		})

		t.Run("a name with - takes an operator out", func(t *testing.T) {
			pack, err := loadPack(t, t.TempDir()).Select([]string{"-SWAP_FIELDS"})

			require.NoError(t, err)
			require.Contains(t, operatorsOf(pack), "CONDITIONALS_BOUNDARY")
			require.NotContains(t, operatorsOf(pack), "SWAP_FIELDS")
		})

		t.Run("a name with + adds an operator that is off by default", func(t *testing.T) {
			pack, err := loadPack(t, t.TempDir()).Select([]string{"+ERRORF_WRAP"})

			require.NoError(t, err)
			require.Contains(t, operatorsOf(pack), "CONDITIONALS_BOUNDARY")
			require.Contains(t, operatorsOf(pack), "ERRORF_WRAP")
		})

		t.Run("names without a sign run only those operators", func(t *testing.T) {
			pack, err := loadPack(t, t.TempDir()).Select([]string{"BRANCH_IF", "ERRORF_WRAP"})

			require.NoError(t, err)
			require.ElementsMatch(t, []string{"BRANCH_IF", "ERRORF_WRAP"}, operatorsOf(pack))
		})

		t.Run("an unknown name returns an error that names it", func(t *testing.T) {
			_, err := loadPack(t, t.TempDir()).Select([]string{"-NOT_AN_OPERATOR"})

			require.ErrorContains(t, err, "no operator NOT_AN_OPERATOR")
		})
	})

	t.Run("edits", func(t *testing.T) {
		t.Run("CONDITIONALS_BOUNDARY moves each comparison to its boundary", func(t *testing.T) {
			edits := editsOf(t, "CONDITIONALS_BOUNDARY", "package a\n\nfunc f(a, b int) bool {\n\treturn a < b || a <= b || a > b || a >= b\n}\n")

			require.Equal(t, []string{"a < b -> a <= b", "a <= b -> a < b", "a > b -> a >= b", "a >= b -> a > b"}, edits)
		})

		t.Run("CONDITIONALS_NEGATION negates each comparison", func(t *testing.T) {
			edits := editsOf(t, "CONDITIONALS_NEGATION", "package a\n\nfunc f(a, b int) bool {\n\treturn a == b || a != b || a < b || a >= b || a > b || a <= b\n}\n")

			require.Equal(t, []string{
				"a == b -> a != b", "a != b -> a == b", "a < b -> a >= b", "a >= b -> a < b", "a > b -> a <= b", "a <= b -> a > b",
			}, edits)
		})

		t.Run("ARITHMETIC_BASE changes each arithmetic operator", func(t *testing.T) {
			edits := editsOf(t, "ARITHMETIC_BASE", "package a\n\nfunc f(a, b int) []int {\n\treturn []int{a + b, a - b, a * b, a / b, a % b}\n}\n")

			require.Equal(t, []string{"a + b -> a - b", "a - b -> a + b", "a * b -> a / b", "a / b -> a * b", "a % b -> a * b"}, edits)
		})

		t.Run("ARITHMETIC_BASE skips a + in a zerolog chain that ends in Msg, Msgf or Send, also over more lines", func(t *testing.T) {
			source := "package a\n\nfunc f(a int) {\n\tlog.Info().Msgf(\"%d\", a+1)\n\tr.log(p).\n\t\tInt(\"n\", a+1).\n\t\tMsg(\"x\")\n" +
				"\tlog.Info().Int(\"n\", a+1).Send()\n}\n"

			edits := editsOf(t, "ARITHMETIC_BASE", source)

			require.Empty(t, edits)
		})

		t.Run("INCREMENT_DECREMENT swaps ++ and --", func(t *testing.T) {
			edits := editsOf(t, "INCREMENT_DECREMENT", "package a\n\nfunc f(a, b int) {\n\ta++\n\tb--\n}\n")

			require.Equal(t, []string{"a++ -> a--", "b-- -> b++"}, edits)
		})

		t.Run("INVERT_LOGICAL swaps && and ||", func(t *testing.T) {
			edits := editsOf(t, "INVERT_LOGICAL", "package a\n\nfunc f(a, b bool) []bool {\n\treturn []bool{a && b, a || b}\n}\n")

			require.Equal(t, []string{"a && b -> a || b", "a || b -> a && b"}, edits)
		})

		t.Run("REMOVE_LOGICAL_NOT removes a !", func(t *testing.T) {
			edits := editsOf(t, "REMOVE_LOGICAL_NOT", "package a\n\nfunc f(ok bool) bool {\n\treturn !ok\n}\n")

			require.Equal(t, []string{"!ok -> ok"}, edits)
		})

		t.Run("EXPRESSION_REMOVE makes each side of && true and each side of || false", func(t *testing.T) {
			edits := editsOf(t, "EXPRESSION_REMOVE", "package a\n\nfunc f(a, b bool) []bool {\n\treturn []bool{a && b, a || b}\n}\n")

			require.ElementsMatch(t, []string{"a && b -> true && b", "a && b -> a && true", "a || b -> false || b", "a || b -> a || false"}, edits)
		})

		t.Run("BRANCH_IF, BRANCH_ELSE and BRANCH_CASE skip a body that has only log lines", func(t *testing.T) {
			source := "package a\n\nfunc f(a bool, n int) {\n\tif a {\n\t\tr.log(p).\n\t\t\tInt(\"n\", n).\n\t\t\tMsg(\"x\")\n\t} else {\n\t\tlog.Info().Send()\n\t\tg()\n\t}\n" +
				"\tswitch n {\n\tcase 1:\n\t\t// only a log line\n\t\tlog.Info().Msgf(\"%d\", n)\n\t}\n}\n"

			require.Empty(t, editsOf(t, "BRANCH_IF", source))
			require.Equal(t, []string{"{\n\t\tlog.Info().Send()\n\t\tg()\n\t} -> {}"}, editsOf(t, "BRANCH_ELSE", source))
			require.Empty(t, editsOf(t, "BRANCH_CASE", source))
		})

		t.Run("BRANCH_IF empties the body of an if, and skips a body that is empty", func(t *testing.T) {
			edits := editsOf(t, "BRANCH_IF", "package a\n\nfunc f(a bool) {\n\tif a {\n\t\tg()\n\t}\n\tif a {\n\t}\n}\n")

			require.Equal(t, []string{"{\n\t\tg()\n\t} -> {}"}, edits)
		})

		t.Run("BRANCH_ELSE empties the body of an else, and skips an else if", func(t *testing.T) {
			edits := editsOf(t, "BRANCH_ELSE", "package a\n\nfunc f(a, b bool) {\n\tif a {\n\t\tg()\n\t} else if b {\n\t\th()\n\t} else {\n\t\tk()\n\t}\n}\n")

			require.Equal(t, []string{"{\n\t\tk()\n\t} -> {}"}, edits)
		})

		t.Run("BRANCH_CASE removes the statements of each case", func(t *testing.T) {
			source := "package a\n\nfunc f(a int, v any, c chan int) {\n\tswitch a {\n\tcase 1, 2:\n\t\tg()\n\tdefault:\n\t\th()\n\t}\n" +
				"\tswitch v.(type) {\n\tcase int, string:\n\t\tk()\n\t}\n\tselect {\n\tcase <-c:\n\t\tm()\n\t}\n}\n"

			edits := findEdits(t, "BRANCH_CASE", source)

			require.Len(t, edits, 4)
			require.Equal(t, formatted(t, "package a\n\nfunc f(a int, v any, c chan int) {\n\tswitch a {\n\tcase 1, 2:\n\tdefault:\n\t\th()\n\t}\n"+
				"\tswitch v.(type) {\n\tcase int, string:\n\t\tk()\n\t}\n\tselect {\n\tcase <-c:\n\t\tm()\n\t}\n}\n"), mutated(t, source, edits[0]))
			require.Equal(t, formatted(t, "package a\n\nfunc f(a int, v any, c chan int) {\n\tswitch a {\n\tcase 1, 2:\n\t\tg()\n\tdefault:\n\t}\n"+
				"\tswitch v.(type) {\n\tcase int, string:\n\t\tk()\n\t}\n\tselect {\n\tcase <-c:\n\t\tm()\n\t}\n}\n"), mutated(t, source, edits[1]))
			require.Equal(t, "k()\n", edits[2].Original)
			require.Equal(t, "m()\n", edits[3].Original)
		})

		t.Run("STATEMENT_REMOVE assigns the value to _ in place of the variable", func(t *testing.T) {
			edits := editsOf(t, "STATEMENT_REMOVE", "package a\n\nfunc f(p *P) {\n\tx := g()\n\tx = g()\n\tp.n = x\n}\n")

			require.Equal(t, []string{"x = g() -> _ = g()", "p.n = x -> _ = x"}, edits)
		})

		t.Run("STATEMENT_REMOVE removes a call that stands alone, except a log line and panic", func(t *testing.T) {
			source := "package a\n\nfunc f(done chan struct{}, p *P) {\n\tclose(done)\n\twg.Done()\n\tp.apply(event)\n" +
				"\tlog.Info().Str(\"a\", b).Msg(\"x\")\n\tlog.Info().Msgf(\"x %d\", 1)\n\tlog.Info().Send()\n\tpanic(\"x\")\n" +
				"\tr.log(p).\n\t\tStr(\"a\", b).\n\t\tMsg(\"x\")\n\tdefer mu.Unlock()\n\tgo run()\n}\n"

			edits := editsOf(t, "STATEMENT_REMOVE", source)

			require.Equal(t, []string{"close(done) -> ", "wg.Done() -> ", "p.apply(event) -> "}, edits)
		})

		t.Run("RETURN_ZERO gives each return value the four zero values, for the type filter to choose", func(t *testing.T) {
			edits := editsOf(t, "RETURN_ZERO", "package a\n\nfunc f() (int, error) {\n\treturn n + 1, err\n}\n")

			require.ElementsMatch(t, []string{
				"n + 1 -> nil", "n + 1 -> 0", `n + 1 -> ""`, "n + 1 -> false",
				"err -> nil", "err -> 0", `err -> ""`, "err -> false",
			}, edits)
		})

		t.Run("RETURN_ZERO empties a struct literal, for the type filter to check the slot", func(t *testing.T) {
			edits := editsOf(t, "RETURN_ZERO", "package a\n\nfunc f() (*T, T, error) {\n\treturn &T{n: 1}, pkg.T{n: 1}, nil\n}\n")

			require.Contains(t, edits, "pkg.T{n: 1} -> pkg.T{}")
			require.NotContains(t, edits, "T{n: 1} -> T{}")
		})

		t.Run("RETURN_ERROR_NIL makes each return value nil, for the type filter to find the error", func(t *testing.T) {
			edits := editsOf(t, "RETURN_ERROR_NIL", "package a\n\nfunc f() (*T, error) {\n\treturn t, fmt.Errorf(\"load: %w\", err)\n}\n")

			require.Equal(t, []string{"t -> nil", "fmt.Errorf(\"load: %w\", err) -> nil"}, edits)
		})

		t.Run("RETURN_TRUE makes each return value true, for the type filter to find the bool slot", func(t *testing.T) {
			edits := editsOf(t, "RETURN_TRUE", "package a\n\nfunc f(a, b int) (bool, error) {\n\treturn a < b, nil\n}\n")

			require.Equal(t, []string{"a < b -> true", "nil -> true"}, edits)
		})

		t.Run("INTEGER_INCREMENT and INTEGER_DECREMENT move an integer by one", func(t *testing.T) {
			source := "package a\n\nvar limit = 3\n"

			require.Equal(t, []string{"3 -> (3+1)"}, editsOf(t, "INTEGER_INCREMENT", source))
			require.Equal(t, []string{"3 -> (3-1)"}, editsOf(t, "INTEGER_DECREMENT", source))
		})

		t.Run("RANGE_BREAK stops a range loop before its first item", func(t *testing.T) {
			source := "package a\n\nfunc f(xs []int) {\n\tfor _, x := range xs {\n\t\tg(x)\n\t}\n\tfor i := 0; i < 3; i++ {\n\t\tg(i)\n\t}\n}\n"

			edits := findEdits(t, "RANGE_BREAK", source)

			require.Len(t, edits, 1)
			require.Equal(t, formatted(t, "package a\n\nfunc f(xs []int) {\n\tfor _, x := range xs {\n\t\tbreak\n\t\tg(x)\n\t}\n\tfor i := 0; i < 3; i++ {\n\t\tg(i)\n\t}\n}\n"),
				mutated(t, source, edits[0]))
		})

		t.Run("BREAK_AT_END stops a loop after its first item", func(t *testing.T) {
			source := "package a\n\nfunc f(xs []int) {\n\tfor _, x := range xs {\n\t\tg(x)\n\t}\n}\n"

			edits := findEdits(t, "BREAK_AT_END", source)

			require.Len(t, edits, 1)
			require.Equal(t, formatted(t, "package a\n\nfunc f(xs []int) {\n\tfor _, x := range xs {\n\t\tg(x)\n\t\tbreak\n\t}\n}\n"), mutated(t, source, edits[0]))
		})

		t.Run("BREAK_AT_END skips a loop that ends with return, break or continue", func(t *testing.T) {
			source := "package a\n\nfunc f(xs []int) int {\n\tfor _, x := range xs {\n\t\treturn x\n\t}\n\tfor x := range xs {\n\t\tbreak\n\t}\n" +
				"\tfor i := 0; i < 3; i++ {\n\t\tcontinue\n\t}\n\treturn 0\n}\n"

			require.Empty(t, editsOf(t, "BREAK_AT_END", source))
		})

		t.Run("SWAP_FIELDS swaps the values of each two adjacent keyed elements", func(t *testing.T) {
			source := "package a\n\nvar p = P{\n\tX: 1, // the first\n\tY: Q{A: \"a\", B: \"b\"},\n\tZ: 3,\n}\n"

			edits := editsOf(t, "SWAP_FIELDS", source)

			require.Equal(t, []string{
				"1, // the first\n\tY: Q{A: \"a\", B: \"b\"} -> Q{A: \"a\", B: \"b\"}, // the first\n\tY: 1",
				"Q{A: \"a\", B: \"b\"},\n\tZ: 3 -> 3,\n\tZ: Q{A: \"a\", B: \"b\"}",
				"\"a\", B: \"b\" -> \"b\", B: \"a\"",
			}, edits)
		})

		t.Run("ERRORF_WRAP turns %w into %v", func(t *testing.T) {
			edits := editsOf(t, "ERRORF_WRAP", "package a\n\nfunc f() error {\n\treturn fmt.Errorf(\"load: %w\", err)\n}\n")

			require.Equal(t, []string{"fmt.Errorf(\"load: %w\", err) -> fmt.Errorf(\"load: %v\", err)"}, edits)
		})
	})
}
