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
	return changesOf(findEdits(t, operatorName, source))
}

func pythonEditsOf(t *testing.T, operatorName, source string) []string {
	t.Helper()
	return changesOf(findEditsIn(t, "a.py", operatorName, source))
}

func changesOf(edits []operator.Edit) []string {
	var changes []string
	for _, edit := range edits {
		changes = append(changes, edit.Original+" -> "+edit.Replacement)
	}
	return changes
}

func findEdits(t *testing.T, operatorName, source string) []operator.Edit {
	t.Helper()
	return findEditsIn(t, "a.go", operatorName, source)
}

func findEditsIn(t *testing.T, file, operatorName, source string) []operator.Edit {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, file), source)
	loaded, err := operator.Load(map[string]string{".go": "go", ".py": "python"}[filepath.Ext(file)], root)
	require.NoError(t, err)
	pack, err := loaded.Select([]string{operatorName})
	require.NoError(t, err)

	edits, err := pack.Edits(context.Background(), astgrep.New(root), root, []string{file})

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

type fixedMatches []operator.Match

func (m fixedMatches) Match(context.Context, []operator.Rule, []string) ([]operator.Match, error) {
	return m, nil
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

		t.Run("a .yaml file in the repository can hold several rules, split by --- with a comment", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/nil.yaml"),
				"id: NIL_MAP\nlanguage: go\nrule:\n  pattern: map[$K]$V{}\nfix: nil\n--- # slices\nid: NIL_SLICE\nlanguage: go\nrule:\n  pattern: '[]$T{}'\nfix: nil\n")

			pack := loadPack(t, repository)

			require.Subset(t, operatorsOf(pack), []string{"NIL_MAP", "NIL_SLICE"})
		})

		t.Run("a rule without an id returns an error that names its file", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/bad.yml"), "language: go\nrule:\n  pattern: $A + $B\nfix: $A - $B\n")

			_, err := operator.Load("go", repository)

			require.ErrorContains(t, err, "read .mutants/operators/go/bad.yml: a rule has no id")
		})

		t.Run("a rule of an operator with the id of a skip rule returns an error that names it", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/log.yml"), "id: zerolog\nlanguage: go\nrule:\n  pattern: log.Print($A)\nfix: log.Print()\n")

			_, err := operator.Load("go", repository)

			require.EqualError(t, err, "the skip rule zerolog has the id of a rule of an operator")
		})

		t.Run("a skip rule in the repository with a fix returns an error that names it", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/skip/go/print.yml"), "id: print\nlanguage: go\nrule:\n  pattern: fmt.Println($$$A)\nfix: ''\n")

			_, err := operator.Load("go", repository)

			require.EqualError(t, err, "read .mutants/skip/go/print.yml: the skip rule print has a fix, but a skip rule changes no code")
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
			require.Contains(t, operatorsOf(pack), "NAMED_VALUE_SWAP")
			require.NotContains(t, operatorsOf(pack), "ERROR_CAUSE_REMOVE")
			require.NotContains(t, operatorsOf(pack), "ARGUMENT_EMPTY")
		})

		t.Run("a rule in the repository for an operator that is off by default is also off by default", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/sprintf.yml"), "id: ERROR_CAUSE_REMOVE/sprintf\nlanguage: go\nrule:\n  pattern: fmt.Sprintf($$$A)\nfix: fmt.Sprint($$$A)\n")

			pack, err := loadPack(t, repository).Select(nil)

			require.NoError(t, err)
			require.NotContains(t, operatorsOf(pack), "ERROR_CAUSE_REMOVE")
		})

		t.Run("a name with - takes an operator out", func(t *testing.T) {
			pack, err := loadPack(t, t.TempDir()).Select([]string{"-NAMED_VALUE_SWAP"})

			require.NoError(t, err)
			require.Contains(t, operatorsOf(pack), "CONDITIONALS_BOUNDARY")
			require.NotContains(t, operatorsOf(pack), "NAMED_VALUE_SWAP")
		})

		t.Run("a name with + adds an operator that is off by default", func(t *testing.T) {
			pack, err := loadPack(t, t.TempDir()).Select([]string{"+ERROR_CAUSE_REMOVE"})

			require.NoError(t, err)
			require.Contains(t, operatorsOf(pack), "CONDITIONALS_BOUNDARY")
			require.Contains(t, operatorsOf(pack), "ERROR_CAUSE_REMOVE")
		})

		t.Run("names without a sign run only those operators", func(t *testing.T) {
			pack, err := loadPack(t, t.TempDir()).Select([]string{"BRANCH_IF", "ERROR_CAUSE_REMOVE"})

			require.NoError(t, err)
			require.ElementsMatch(t, []string{"BRANCH_IF", "ERROR_CAUSE_REMOVE"}, operatorsOf(pack))
		})

		t.Run("none runs no operator, and a name with + after it adds one", func(t *testing.T) {
			pack := loadPack(t, t.TempDir())

			none, err := pack.Select([]string{operator.None})
			require.NoError(t, err)
			oneMore, err := pack.Select([]string{operator.None, "+ERROR_CAUSE_REMOVE"})
			require.NoError(t, err)

			require.Empty(t, operatorsOf(none))
			require.Equal(t, []string{"ERROR_CAUSE_REMOVE"}, operatorsOf(oneMore))
		})

		t.Run("a name without a sign runs its operator in each pack that has it, and no default operator in the other packs", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/NIL_MAP.yml"), "id: NIL_MAP\nlanguage: go\nrule:\n  pattern: map[$K]$V{}\nfix: nil\n")
			inPython, err := operator.Load("python", repository)
			require.NoError(t, err)

			packs, err := operator.Select([]operator.Pack{loadPack(t, repository), inPython}, []string{"NIL_MAP"})

			require.NoError(t, err)
			require.Equal(t, [][]string{{"NIL_MAP"}, nil}, [][]string{operatorsOf(packs[0]), operatorsOf(packs[1])})
		})

		t.Run("a name with a sign changes only the packs that have its operator", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/NIL_MAP.yml"), "id: NIL_MAP\nlanguage: go\nrule:\n  pattern: map[$K]$V{}\nfix: nil\n")
			inPython, err := operator.Load("python", repository)
			require.NoError(t, err)

			packs, err := operator.Select([]operator.Pack{loadPack(t, repository), inPython}, []string{"-NIL_MAP"})

			require.NoError(t, err)
			require.NotContains(t, operatorsOf(packs[0]), "NIL_MAP")
			require.Contains(t, operatorsOf(packs[1]), "CONDITIONALS_BOUNDARY")
		})

		t.Run("a name that no pack has returns an error that names it", func(t *testing.T) {
			inPython, err := operator.Load("python", t.TempDir())
			require.NoError(t, err)

			_, err = operator.Select([]operator.Pack{loadPack(t, t.TempDir()), inPython}, []string{"+NIL_MAP"})

			require.EqualError(t, err, "mutants has no operator NIL_MAP: mutants operators lists the operators")
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

		t.Run("each operator skips its edits inside a zerolog chain", func(t *testing.T) {
			source := "package a\n\nfunc f(a, b int) bool {\n\tlog.Info().Bool(\"late\", a < b).Msg(\"x\")\n\treturn a < b\n}\n"

			edits := editsOf(t, "CONDITIONALS_BOUNDARY", source)

			require.Equal(t, []string{"a < b -> a <= b"}, edits)
		})

		t.Run("a skip rule in the repository skips the edits inside its matches", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/skip/go/slog.yml"), "id: slog\nlanguage: go\nrule:\n  kind: call_expression\n  has:\n    field: function\n    regex: ^slog\\.Info$\n")
			writeFile(t, filepath.Join(repository, "a.go"), "package a\n\nfunc f(a int) int {\n\tslog.Info(\"total\", \"n\", a+1)\n\treturn a + 1\n}\n")
			pack, err := loadPack(t, repository).Select([]string{"ARITHMETIC_BASE"})
			require.NoError(t, err)

			edits, err := pack.Edits(context.Background(), astgrep.New(repository), repository, []string{"a.go"})

			require.NoError(t, err)
			require.Equal(t, []string{"a + 1 -> a - 1"}, changesOf(edits))
		})

		t.Run("a skip rule in the repository with the id of a standard skip rule replaces it", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/skip/go/zerolog.yml"), "id: zerolog\nlanguage: go\nrule:\n  pattern: log.Print($$$A)\n")
			writeFile(t, filepath.Join(repository, "a.go"), "package a\n\nfunc f(a int) {\n\tlog.Info().Int(\"n\", a+1).Msg(\"x\")\n}\n")
			pack, err := loadPack(t, repository).Select([]string{"ARITHMETIC_BASE"})
			require.NoError(t, err)

			edits, err := pack.Edits(context.Background(), astgrep.New(repository), repository, []string{"a.go"})

			require.NoError(t, err)
			require.Equal(t, []string{"a+1 -> a - 1"}, changesOf(edits))
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
			require.Equal(t, formatted(t, "package a\n\nfunc f(a int, v any, c chan int) {\n\tswitch a {\n\tcase 1, 2:\n\t\tg()\n\tdefault:\n\t\th()\n\t}\n"+
				"\tswitch v.(type) {\n\tcase int, string:\n\t}\n\tselect {\n\tcase <-c:\n\t\tm()\n\t}\n}\n"), mutated(t, source, edits[2]))
			require.Equal(t, formatted(t, "package a\n\nfunc f(a int, v any, c chan int) {\n\tswitch a {\n\tcase 1, 2:\n\t\tg()\n\tdefault:\n\t\th()\n\t}\n"+
				"\tswitch v.(type) {\n\tcase int, string:\n\t\tk()\n\t}\n\tselect {\n\tcase <-c:\n\t}\n}\n"), mutated(t, source, edits[3]))
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

		t.Run("RETURN_EMPTY gives each return value the four zero values, for the type filter to choose", func(t *testing.T) {
			edits := editsOf(t, "RETURN_EMPTY", "package a\n\nfunc f() (int, error) {\n\treturn n + 1, err\n}\n")

			require.ElementsMatch(t, []string{
				"n + 1 -> nil", "n + 1 -> 0", `n + 1 -> ""`, "n + 1 -> false",
				"err -> nil", "err -> 0", `err -> ""`, "err -> false",
			}, edits)
		})

		t.Run("RETURN_EMPTY empties a struct literal, for the type filter to check the slot", func(t *testing.T) {
			edits := editsOf(t, "RETURN_EMPTY", "package a\n\nfunc f() (*T, T, error) {\n\treturn &T{n: 1}, pkg.T{n: 1}, nil\n}\n")

			require.Contains(t, edits, "pkg.T{n: 1} -> pkg.T{}")
			require.NotContains(t, edits, "T{n: 1} -> T{}")
		})

		t.Run("ERROR_REMOVE makes each return value nil, for the type filter to find the error", func(t *testing.T) {
			edits := editsOf(t, "ERROR_REMOVE", "package a\n\nfunc f() (*T, error) {\n\treturn t, fmt.Errorf(\"load: %w\", err)\n}\n")

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

		t.Run("BREAK_AT_START stops a range loop before its first item", func(t *testing.T) {
			source := "package a\n\nfunc f(xs []int) {\n\tfor _, x := range xs {\n\t\tg(x)\n\t}\n\tfor i := 0; i < 3; i++ {\n\t\tg(i)\n\t}\n}\n"

			edits := findEdits(t, "BREAK_AT_START", source)

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

		t.Run("NAMED_VALUE_SWAP swaps the values of each two adjacent keyed elements", func(t *testing.T) {
			source := "package a\n\nvar p = P{\n\tX: 1, // the first\n\tY: Q{A: \"a\", B: \"b\"},\n\tZ: 3,\n}\n"

			edits := editsOf(t, "NAMED_VALUE_SWAP", source)

			require.Equal(t, []string{
				"1, // the first\n\tY: Q{A: \"a\", B: \"b\"} -> Q{A: \"a\", B: \"b\"}, // the first\n\tY: 1",
				"Q{A: \"a\", B: \"b\"},\n\tZ: 3 -> 3,\n\tZ: Q{A: \"a\", B: \"b\"}",
				"\"a\", B: \"b\" -> \"b\", B: \"a\"",
			}, edits)
		})

		t.Run("CONDITIONALS_BOUNDARY moves the boundary of After and Before, also under a !", func(t *testing.T) {
			edits := editsOf(t, "CONDITIONALS_BOUNDARY", "package a\n\nfunc f(a, b time.Time) []bool {\n\treturn []bool{a.After(b), a.Before(b), !a.After(b), !s.next().Before(b)}\n}\n")

			require.Equal(t, []string{
				"a.After(b) -> !a.Before(b)", "a.Before(b) -> !a.After(b)", "!a.After(b) -> a.Before(b)", "!s.next().Before(b) -> s.next().After(b)",
			}, edits)
		})

		t.Run("NAMED_VALUE_REMOVE removes each keyed element with its comma", func(t *testing.T) {
			source := "package a\n\nvar p = P{A: 1, B: two,\n\tC: Q{D: 3},\n}\n"

			edits := findEdits(t, "NAMED_VALUE_REMOVE", source)

			require.Len(t, edits, 4)
			require.Equal(t, formatted(t, "package a\n\nvar p = P{B: two,\n\tC: Q{D: 3},\n}\n"), mutated(t, source, edits[0]))
			require.Equal(t, formatted(t, "package a\n\nvar p = P{A: 1,\n\tC: Q{D: 3},\n}\n"), mutated(t, source, edits[1]))
			require.Equal(t, formatted(t, "package a\n\nvar p = P{A: 1, B: two}\n"), mutated(t, source, edits[2]))
			require.Equal(t, formatted(t, "package a\n\nvar p = P{A: 1, B: two,\n\tC: Q{},\n}\n"), mutated(t, source, edits[3]))
		})

		t.Run("ARGUMENT_EMPTY gives each argument the zero values, for the type filter to choose, and skips a log chain", func(t *testing.T) {
			source := "package a\n\nfunc f() {\n\tresolve(id.String(), Point{X: 1})\n\tlog.Info().Str(\"id\", id.String()).Msg(\"x\")\n}\n"

			edits := editsOf(t, "ARGUMENT_EMPTY", source)

			require.ElementsMatch(t, []string{
				"id.String() -> nil", "id.String() -> 0", `id.String() -> ""`, "id.String() -> false",
				"Point{X: 1} -> nil", "Point{X: 1} -> 0", `Point{X: 1} -> ""`, "Point{X: 1} -> false", "Point{X: 1} -> Point{}",
			}, edits)
		})

		t.Run("NAMED_VALUE_SWAP also swaps two values with a block comment between them", func(t *testing.T) {
			edits := editsOf(t, "NAMED_VALUE_SWAP", "package a\n\nvar p = P{X: 1, /* c */ Y: 2}\n")

			require.Equal(t, []string{"1, /* c */ Y: 2 -> 2, /* c */ Y: 1"}, edits)
		})

		t.Run("ERROR_CAUSE_REMOVE skips an Errorf with no %w", func(t *testing.T) {
			edits := editsOf(t, "ERROR_CAUSE_REMOVE", "package a\n\nfunc f() error {\n\treturn fmt.Errorf(\"load %d: %v\", n, err)\n}\n")

			require.Empty(t, edits)
		})

		t.Run("each edit takes its text from its own file", func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "a.go"), "package a\n\nvar x = 1 + 2\n")
			writeFile(t, filepath.Join(root, "b/b.go"), "package b\n\nfunc f(n int) int {\n\treturn n + 10\n}\n")
			pack, err := loadPack(t, root).Select([]string{"ARITHMETIC_BASE"})
			require.NoError(t, err)

			edits, err := pack.Edits(context.Background(), astgrep.New(root), root, []string{"a.go", "b/b.go"})

			require.NoError(t, err)
			require.Equal(t, []operator.Edit{
				{File: "a.go", Operator: "ARITHMETIC_BASE", Rule: "ARITHMETIC_BASE/plus", Start: 19, End: 24, Original: "1 + 2", Replacement: "1 - 2"},
				{File: "b/b.go", Operator: "ARITHMETIC_BASE", Rule: "ARITHMETIC_BASE/plus", Start: 39, End: 45, Original: "n + 10", Replacement: "n - 10"},
			}, edits)
		})

		t.Run("a rule that ast-grep cannot read returns the reason from ast-grep", func(t *testing.T) {
			repository := t.TempDir()
			writeFile(t, filepath.Join(repository, ".mutants/operators/go/bad.yml"), "id: BAD\nlanguage: go\nrule:\n  kind: not_a_kind\nfix: x\n")
			writeFile(t, filepath.Join(repository, "a.go"), "package a\n")
			pack, err := loadPack(t, repository).Select([]string{"BAD"})
			require.NoError(t, err)

			_, err = pack.Edits(context.Background(), astgrep.New(repository), repository, []string{"a.go"})

			require.ErrorContains(t, err, "not_a_kind")
		})

		t.Run("a match that ends past the end of its file returns an error that names it", func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "a.go"), "package a\n")
			pack, err := loadPack(t, root).Select([]string{"CONDITIONALS_BOUNDARY"})
			require.NoError(t, err)
			matcher := fixedMatches{{Rule: "CONDITIONALS_BOUNDARY/lt", File: "a.go", Start: 5, End: 50}}

			_, err = pack.Edits(context.Background(), matcher, root, []string{"a.go"})

			require.ErrorContains(t, err, "the match of CONDITIONALS_BOUNDARY/lt in a.go at bytes 5 to 50 is not in the file")
		})

		t.Run("ERROR_CAUSE_REMOVE turns %w into %v", func(t *testing.T) {
			edits := editsOf(t, "ERROR_CAUSE_REMOVE", "package a\n\nfunc f() error {\n\treturn fmt.Errorf(\"load: %w\", err)\n}\n")

			require.Equal(t, []string{"fmt.Errorf(\"load: %w\", err) -> fmt.Errorf(\"load: %v\", err)"}, edits)
		})
	})

	t.Run("python edits", func(t *testing.T) {
		t.Run("CONDITIONALS_BOUNDARY moves a comparison of two operands to its boundary, and skips a chain", func(t *testing.T) {
			edits := pythonEditsOf(t, "CONDITIONALS_BOUNDARY", "def f(a, b):\n    return a < b or a <= b or a > b or a >= b or 0 < a < 9\n")

			require.Equal(t, []string{"a < b -> a <= b", "a <= b -> a < b", "a > b -> a >= b", "a >= b -> a > b"}, edits)
		})

		t.Run("CONDITIONALS_NEGATION negates each comparison, also is and in", func(t *testing.T) {
			edits := pythonEditsOf(t, "CONDITIONALS_NEGATION", "def f(a, b):\n    return [a == b, a != b, a < b, a >= b, a > b, a <= b, a is b, a is not b, a in b, a not in b]\n")

			require.Equal(t, []string{
				"a == b -> a != b", "a != b -> a == b", "a < b -> a >= b", "a >= b -> a < b", "a > b -> a <= b", "a <= b -> a > b",
				"a is b -> a is not b", "a is not b -> a is b", "a in b -> a not in b", "a not in b -> a in b",
			}, edits)
		})

		t.Run("ARITHMETIC_BASE changes each arithmetic operator, and skips the % of a format string and a + or * with a string", func(t *testing.T) {
			edits := pythonEditsOf(t, "ARITHMETIC_BASE", "def f(a, b):\n    return [a + b, a - b, a * b, a / b, a // b, a % b, \"%s\" % a, \"a\" + b, \"-\" * b]\n")

			require.Equal(t, []string{"a + b -> a - b", "a - b -> a + b", "a * b -> a / b", "a / b -> a * b", "a // b -> a * b", "a % b -> a * b"}, edits)
		})

		t.Run("INCREMENT_DECREMENT swaps += and -=", func(t *testing.T) {
			edits := pythonEditsOf(t, "INCREMENT_DECREMENT", "def f(a, b):\n    a += b\n    a -= 1\n")

			require.Equal(t, []string{"a += b -> a -= b", "a -= 1 -> a += 1"}, edits)
		})

		t.Run("INVERT_LOGICAL swaps and and or", func(t *testing.T) {
			edits := pythonEditsOf(t, "INVERT_LOGICAL", "def f(a, b):\n    return [a and b, a or b]\n")

			require.Equal(t, []string{"a and b -> a or b", "a or b -> a and b"}, edits)
		})

		t.Run("EXPRESSION_REMOVE makes each side of and True and each side of or False", func(t *testing.T) {
			edits := pythonEditsOf(t, "EXPRESSION_REMOVE", "def f(a, b):\n    return [a and b, a or b]\n")

			require.ElementsMatch(t, []string{"a and b -> True and b", "a and b -> a and True", "a or b -> False or b", "a or b -> a or False"}, edits)
		})

		t.Run("REMOVE_LOGICAL_NOT removes a not, and skips not in", func(t *testing.T) {
			edits := pythonEditsOf(t, "REMOVE_LOGICAL_NOT", "def f(a, b):\n    return [not a, a not in b]\n")

			require.Equal(t, []string{"not a -> a"}, edits)
		})

		t.Run("INTEGER_INCREMENT and INTEGER_DECREMENT move an integer by one", func(t *testing.T) {
			source := "def f(xs):\n    return xs[0] + 1_000\n"

			require.Equal(t, []string{"0 -> (0+1)", "1_000 -> (1_000+1)"}, pythonEditsOf(t, "INTEGER_INCREMENT", source))
			require.Equal(t, []string{"0 -> (0-1)", "1_000 -> (1_000-1)"}, pythonEditsOf(t, "INTEGER_DECREMENT", source))
		})

		t.Run("INTEGER_INCREMENT and INTEGER_DECREMENT skip a number in a case pattern, where (0+1) does not compile, and keep a number in a guard", func(t *testing.T) {
			source := "def f(a):\n    match a:\n        case 0 | [1, -2] | {\"k\": 3} | Point(x=4):\n            pass\n        case n if n > 5:\n            pass\n"

			require.Equal(t, []string{"5 -> (5+1)"}, pythonEditsOf(t, "INTEGER_INCREMENT", source))
			require.Equal(t, []string{"5 -> (5-1)"}, pythonEditsOf(t, "INTEGER_DECREMENT", source))
		})

		t.Run("BRANCH_IF puts pass in place of the body of an if and of an elif", func(t *testing.T) {
			edits := pythonEditsOf(t, "BRANCH_IF", "def f(a):\n    if a > 1:\n        g(a)\n        h(a)\n    elif a: return a\n")

			require.Equal(t, []string{"g(a)\n        h(a) -> pass", "return a -> pass"}, edits)
		})

		t.Run("BRANCH_ELSE puts pass in place of the body of the else of an if, and skips the else of a loop", func(t *testing.T) {
			edits := pythonEditsOf(t, "BRANCH_ELSE", "def f(a):\n    if a:\n        g(a)\n    else:\n        h(a)\n    for x in a:\n        g(x)\n    else:\n        h(a)\n")

			require.Equal(t, []string{"h(a) -> pass"}, edits)
		})

		t.Run("BRANCH_CASE puts pass in place of the body of each case", func(t *testing.T) {
			edits := pythonEditsOf(t, "BRANCH_CASE", "def f(a):\n    match a:\n        case 1:\n            g(a)\n        case _:\n            h(a)\n")

			require.Equal(t, []string{"g(a) -> pass", "h(a) -> pass"}, edits)
		})

		t.Run("STATEMENT_REMOVE assigns the value to _ in a function, and puts pass in place of a call that stands alone", func(t *testing.T) {
			edits := pythonEditsOf(t, "STATEMENT_REMOVE", "LIMIT = 10\n\n\nasync def f(self, client):\n    self.total = 0\n    g(self)\n    await client.close()\n")

			require.Equal(t, []string{"self.total = 0 -> _ = 0", "g(self) -> pass", "await client.close() -> pass"}, edits)
		})

		t.Run("RETURN_EMPTY gives each return value the empty values, for the filter of the adapter to choose", func(t *testing.T) {
			edits := pythonEditsOf(t, "RETURN_EMPTY", "def f(a):\n    return a.total\n")

			require.ElementsMatch(t, []string{
				"a.total -> None", "a.total -> 0", `a.total -> ""`, "a.total -> False", "a.total -> []", "a.total -> {}",
			}, edits)
		})

		t.Run("RETURN_TRUE makes each return value True", func(t *testing.T) {
			edits := findEditsIn(t, "a.py", "RETURN_TRUE", "def f(a, b):\n    if a:\n        return a < b\n    return g(b)\n")

			require.Equal(t, []operator.Edit{
				{File: "a.py", Operator: "RETURN_TRUE", Rule: "RETURN_TRUE/condition", Start: 38, End: 43, Original: "a < b", Replacement: "True"},
				{File: "a.py", Operator: "RETURN_TRUE", Rule: "RETURN_TRUE/annotated", Start: 55, End: 59, Original: "g(b)", Replacement: "True"},
			}, edits)
		})

		t.Run("ERROR_REMOVE puts return in place of a raise in a function, and skips a raise outside a function", func(t *testing.T) {
			edits := pythonEditsOf(t, "ERROR_REMOVE", "def f(a):\n    if not a:\n        raise ValueError(a)\n\n\nraise SystemExit(1)\n")

			require.Equal(t, []string{"raise ValueError(a) -> return"}, edits)
		})

		t.Run("ERROR_CAUSE_REMOVE removes the cause of a raise, and skips from None", func(t *testing.T) {
			edits := pythonEditsOf(t, "ERROR_CAUSE_REMOVE", "def f(a):\n    try:\n        g(a)\n    except KeyError as err:\n        raise ValueError(a) from err\n    except TypeError:\n        raise ValueError(a) from None\n")

			require.Equal(t, []string{"raise ValueError(a) from err -> raise ValueError(a)"}, edits)
		})

		t.Run("BREAK_AT_START stops a for loop before its first item", func(t *testing.T) {
			source := "def f(xs):\n    for x in xs:\n        g(x)\n        h(x)\n"

			edits := findEditsIn(t, "a.py", "BREAK_AT_START", source)

			require.Len(t, edits, 1)
			require.Equal(t, "def f(xs):\n    for x in xs:\n        break\n        g(x)\n        h(x)\n", source[:edits[0].Start]+edits[0].Replacement+source[edits[0].End:])
		})

		t.Run("BREAK_AT_END stops a for or while loop after its first item", func(t *testing.T) {
			source := "def f(xs):\n    for x in xs:\n        g(x)\n"

			edits := findEditsIn(t, "a.py", "BREAK_AT_END", source)

			require.Len(t, edits, 1)
			require.Equal(t, "def f(xs):\n    for x in xs:\n        g(x)\n        break\n", source[:edits[0].Start]+edits[0].Replacement+source[edits[0].End:])
		})

		t.Run("BREAK_AT_END skips a loop that ends with return, break, continue or raise", func(t *testing.T) {
			source := "def f(xs):\n    for x in xs:\n        return x\n    while xs:\n        break\n    for x in xs:\n        continue\n    while xs:\n        raise ValueError(xs)\n"

			require.Empty(t, pythonEditsOf(t, "BREAK_AT_END", source))
		})

		t.Run("NAMED_VALUE_REMOVE removes each keyword argument with its comma", func(t *testing.T) {
			source := "def f(a):\n    return Totals(a, paid=a.paid, owed=a.owed)\n"

			edits := findEditsIn(t, "a.py", "NAMED_VALUE_REMOVE", source)

			require.Len(t, edits, 2)
			require.Equal(t, "def f(a):\n    return Totals(a,  owed=a.owed)\n", source[:edits[0].Start]+edits[0].Replacement+source[edits[0].End:])
			require.Equal(t, "def f(a):\n    return Totals(a, paid=a.paid, )\n", source[:edits[1].Start]+edits[1].Replacement+source[edits[1].End:])
		})

		t.Run("NAMED_VALUE_SWAP swaps the values of two keyword arguments next to each other, also with a # comment between them", func(t *testing.T) {
			edits := pythonEditsOf(t, "NAMED_VALUE_SWAP", "def f(a):\n    return Totals(\n        paid=a.paid,  # the paid part\n        owed=a.owed,\n    )\n")

			require.Equal(t, []string{"a.paid,  # the paid part\n        owed=a.owed -> a.owed,  # the paid part\n        owed=a.paid"}, edits)
		})

		t.Run("ARGUMENT_EMPTY gives each argument None, and skips *args and **kwargs", func(t *testing.T) {
			edits := pythonEditsOf(t, "ARGUMENT_EMPTY", "def f(a, args, kwargs):\n    g(a.id, *args, key=a.key, **kwargs)\n")

			require.Equal(t, []string{"a.id -> None", "a.key -> None"}, edits)
		})

		t.Run("each operator skips its edits in a log call, a type annotation and a TYPE_CHECKING block", func(t *testing.T) {
			source := "from typing import TYPE_CHECKING, Literal\n\nif TYPE_CHECKING:\n    LIMIT = 1\n\n\n" +
				"def f(n: Literal[1]) -> int:\n    logger.info(\"n %d\", n + 1)\n    self.log.debug(n + 1)\n    return n + 2\n"

			require.Equal(t, []string{"2 -> (2+1)"}, pythonEditsOf(t, "INTEGER_INCREMENT", source))
		})

		t.Run("each operator skips the test files and the generated files", func(t *testing.T) {
			root := t.TempDir()
			for _, file := range []string{"test_cart.py", "cart_test.py", "conftest.py", "tests/helpers.py", "cart_pb2.py", "cart.py"} {
				writeFile(t, filepath.Join(root, file), "LIMIT = 1\n")
			}
			writeFile(t, filepath.Join(root, "cart_pb2.py"), "# -*- coding: utf-8 -*-\n# Generated by the protocol buffer compiler.  DO NOT EDIT!\nLIMIT = 1\n")
			loaded, err := operator.Load("python", root)
			require.NoError(t, err)
			pack, err := loaded.Select([]string{"INTEGER_INCREMENT"})
			require.NoError(t, err)

			edits, err := pack.Edits(context.Background(), astgrep.New(root), root, []string{"test_cart.py", "cart_test.py", "conftest.py", "tests/helpers.py", "cart_pb2.py", "cart.py"})

			require.NoError(t, err)
			require.Equal(t, []operator.Edit{
				{File: "cart.py", Operator: "INTEGER_INCREMENT", Rule: "INTEGER_INCREMENT", Start: 8, End: 9, Original: "1", Replacement: "(1+1)"},
			}, edits)
		})
	})
}
