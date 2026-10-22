//go:build unit

package elixir_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/language/elixir"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/stretchr/testify/require"
)

const mixProject = "defmodule Shop.MixProject do\n  use Mix.Project\n\n  def project, do: [app: :shop, version: \"0.1.0\", deps: []]\nend\n"

const discount = `defmodule Shop.Discount do
  @limit 100

  def discount(total) do
    if total >= @limit do
      10
    else
      0
    end
  end

  def unused(total), do: total + 1
end
`

const discountTest = `defmodule Shop.DiscountTest do
  use ExUnit.Case

  test "low" do
    assert Shop.Discount.discount(50) == 0
  end

  test "edge" do
    assert Shop.Discount.discount(100) == 10
  end
end
`

const secondRunFailsTest = `defmodule Shop.SecondRunTest do
  use ExUnit.Case

  test "second run" do
    path = System.fetch_env!("MUTANTS_TEST_COUNTER")

    count =
      case File.read(path) do
        {:ok, text} -> String.to_integer(text) + 1
        {:error, _} -> 1
      end

    File.write!(path, Integer.to_string(count))
    assert count != 2
    assert Shop.Discount.discount(50) == 0
  end
end
`

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
		"mix.exs":                     mixProject,
		"lib/shop/discount.ex":        discount,
		"test/test_helper.exs":        "ExUnit.start()\n",
		"test/shop/discount_test.exs": discountTest,
	}
	for name, content := range extra {
		files[name] = content
	}
	return files
}

func newAdapter(root string) language.Adapter {
	return elixir.New(root, elixir.Settings{Workers: 2})
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
		Language:    "elixir",
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
			root := newProject(t, map[string]string{"a.ex": "defmodule A do\n  def f, do: nil\nend\n"})

			require.False(t, newAdapter(root).Keep(editOf(t, root, "a.ex", "RETURN_EMPTY/nil", "nil", "nil")))
		})

		t.Run("keeps the RETURN_EMPTY value that fits the @spec of the function, false for a name that ends in ?, and nil with no @spec", func(t *testing.T) {
			source := `defmodule A do
  @spec count() :: non_neg_integer()
  def count, do: n()
  @spec name() :: String.t()
  def name, do: s()
  @spec ok() :: boolean
  def ok, do: b()
  @spec items() :: [integer]
  def items, do: xs()
  @spec table() :: %{optional(atom) => integer}
  def table, do: d()
  @spec maybe() :: integer | nil
  def maybe, do: m()
  @spec cart() :: Cart.t()
  def cart, do: c()
  def plain, do: p()
  def empty?, do: blank()
end
`
			root := newProject(t, map[string]string{"a.ex": source})
			adapter := newAdapter(root)
			kept := map[string]string{}
			for _, original := range []string{"n()", "s()", "b()", "xs()", "d()", "m()", "c()", "p()", "blank()"} {
				for _, empty := range []string{"nil", "0", `""`, "false", "[]", "%{}"} {
					if adapter.Keep(editOf(t, root, "a.ex", "RETURN_EMPTY/x", original, empty)) {
						kept[original] = empty
					}
				}
			}

			require.Equal(t, map[string]string{
				"n()": "0", "s()": `""`, "b()": "false", "xs()": "[]", "d()": "%{}", "m()": "nil", "c()": "nil", "p()": "nil", "blank()": "false",
			}, kept)
		})

		t.Run("keeps a RETURN_TRUE edit of a value that is not a condition only in a function that returns a boolean", func(t *testing.T) {
			source := `defmodule A do
  @spec ok(term) :: boolean()
  def ok(a), do: check(a)
  def valid?(a), do: verify(a)
  @spec count(term) :: integer
  def count(a), do: size(a)
  def plain(a), do: load(a)
end
`
			root := newProject(t, map[string]string{"a.ex": source})
			adapter := newAdapter(root)
			kept := map[string]bool{}
			for _, original := range []string{"check(a)", "verify(a)", "size(a)", "load(a)"} {
				kept[original] = adapter.Keep(editOf(t, root, "a.ex", "RETURN_TRUE/annotated", original, "true"))
			}

			require.Equal(t, map[string]bool{"check(a)": true, "verify(a)": true, "size(a)": false, "load(a)": false}, kept)
		})

		t.Run("keeps a STATEMENT_REMOVE of a binding only when its variable has a value before it in its function", func(t *testing.T) {
			source := `defmodule A do
  def sweep(items, dry_run) do
    total = 0
    total = total + 1
    {:ok, first} = fetch(items)
    first = first + 1
    dry_run = true
    case items do
      [head | _] ->
        head = head * 2
        head
    end
    other = 1
    other
  end
end
`
			root := newProject(t, map[string]string{"a.ex": source})
			adapter := newAdapter(root)
			kept := map[string]bool{}
			for _, binding := range []string{"total = 0", "total = total + 1", "first = first + 1", "dry_run = true", "head = head * 2", "other = 1"} {
				_, value, _ := strings.Cut(binding, " = ")
				kept[binding] = adapter.Keep(editOf(t, root, "a.ex", "STATEMENT_REMOVE/assign", binding, "_ = "+value))
			}

			require.Equal(t, map[string]bool{
				"total = 0": false, "total = total + 1": true, "first = first + 1": true, "dry_run = true": true, "head = head * 2": true, "other = 1": false,
			}, kept)
		})
		t.Run("drops a NAMED_VALUE_REMOVE of a struct field whose value is the literal default of the field in defstruct", func(t *testing.T) {
			money := "defmodule Shop.Money do\n  defstruct [:id, cents: 0, currency: :aud, created: System.os_time()]\nend\n"
			cart := `defmodule Shop.Cart do
  alias Shop.Money, as: M
  defstruct [:id, total: 0]

  defmodule Line do
    defstruct qty: 1
  end

  def new, do: %__MODULE__{total: 0, id: nil}
  def priced, do: %__MODULE__{total: 5, id: 1}
  def line, do: %Line{qty: 1}
  def money, do: %M{cents: 0, currency: :usd, created: 1}
  def map, do: %{total: 0}
end
`
			root := newProject(t, map[string]string{"lib/shop/money.ex": money, "lib/shop/cart.ex": cart})
			adapter := newAdapter(root)
			kept := map[string]bool{}
			for context, original := range map[string]string{
				"{total: 0, id: nil}":                    ", id: nil",
				"{total: 5, id: 1}":                      "total: 5,",
				"%Line{qty: 1}":                          "qty: 1",
				"{cents: 0, currency: :usd, created: 1}": "cents: 0,",
				"currency: :usd, created: 1}":            "currency: :usd,",
				"created: 1}":                            "created: 1",
				"%{total: 0}":                            "total: 0",
			} {
				kept[original] = adapter.Keep(editIn(t, root, "lib/shop/cart.ex", "NAMED_VALUE_REMOVE", context, original, ""))
			}

			require.Equal(t, map[string]bool{
				", id: nil": false, "total: 5,": true, "qty: 1": false, "cents: 0,": false, "currency: :usd,": true, "created: 1": true, "total: 0": true,
			}, kept)
		})
	})

	t.Run("test file", func(t *testing.T) {
		t.Run("takes the files that the skip rule test-file skips", func(t *testing.T) {
			adapter := elixir.New(t.TempDir(), elixir.Settings{})
			taken := map[string]bool{}
			for _, file := range []string{"test/support/factory.ex", "apps/shop/test/support/case.ex", "lib/shop/order.ex", "lib/shop/test_helpers.ex"} {
				taken[file] = adapter.TestFile(file)
			}

			require.Equal(t, map[string]bool{
				"test/support/factory.ex": true, "apps/shop/test/support/case.ex": true,
				"lib/shop/order.ex": false, "lib/shop/test_helpers.ex": false,
			}, taken)
		})
	})

	t.Run("function", func(t *testing.T) {
		t.Run("names the modules from the outside in, and the function with its arity", func(t *testing.T) {
			source := `defmodule Shop do
  @limit 1

  defmodule Cart do
    def total(items, discount \\ 0) do
      Enum.sum(items) - discount
    end

    defp empty?, do: true
  end
end

defimpl String.Chars, for: Shop.Cart do
  def to_string(_cart), do: "cart"
end
`
			root := newProject(t, map[string]string{"a.ex": source})
			adapter := newAdapter(root)

			require.Equal(t, []string{"Shop", "Shop.Cart.total/2", "Shop.Cart.empty?/0", "String.Chars.Shop.Cart.to_string/1"}, []string{
				adapter.Function("a.ex", strings.Index(source, "1")),
				adapter.Function("a.ex", strings.Index(source, "Enum.sum")),
				adapter.Function("a.ex", strings.Index(source, "true")),
				adapter.Function("a.ex", strings.Index(source, `"cart"`)),
			})
		})
	})

	t.Run("uncovered", func(t *testing.T) {
		t.Run("a mutant on a line that no test runs is NOT COVERED, and a mutant on a line that a test runs is not", func(t *testing.T) {
			root := newProject(t, shop(nil))
			unused := mutantOf(t, root, "lib/shop/discount.ex", "total + 1", "total - 1")
			used := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total > @limit")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{unused, used}, diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{unused.ID: ""}, uncovered)
		})

		t.Run("a mutant in a condition or a guard that a test runs is not NOT COVERED, also when no test runs the body of its clause", func(t *testing.T) {
			source := `defmodule Shop.Kind do
  def size(x) do
    y = x * 2
    cond do
      y == 0 -> :zero
      true -> :other
    end
  end

  def count(x) do
    case x do
      n when n > 5 -> :many
      _ -> :few
    end
  end

  def order(a, b) when a < b, do: :less
  def order(_a, _b), do: :more
end
`
			test := "defmodule Shop.KindTest do\n  use ExUnit.Case\n\n  test \"kind\" do\n    assert Shop.Kind.size(1) == :other\n" +
				"    assert Shop.Kind.count(1) == :few\n    assert Shop.Kind.order(2, 1) == :more\n  end\nend\n"
			root := newProject(t, shop(map[string]string{"lib/shop/kind.ex": source, "test/shop/kind_test.exs": test}))
			var conditions, bodies []mutant.Mutant
			for _, original := range []string{"y == 0", "n > 5", "a < b"} {
				conditions = append(conditions, mutantOf(t, root, "lib/shop/kind.ex", original, "true"))
			}
			for _, original := range []string{":zero", ":many", ":less"} {
				bodies = append(bodies, mutantOf(t, root, "lib/shop/kind.ex", original, "nil"))
			}

			uncovered, err := newAdapter(root).Uncovered(context.Background(), append(conditions, bodies...), diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{bodies[0].ID: "", bodies[1].ID: "", bodies[2].ID: ""}, uncovered)
		})

		t.Run("a mutant on a line that :cover does not count, such as a tuple of two elements, is NOT COVERED only when no test runs its function clause", func(t *testing.T) {
			source := `defmodule Shop.Split do
  def split(x, drop) do
    signals =
      cond do
        x == 0 -> [:zero]
        drop > 0 -> [:rounded]
        true -> [:exact]
      end

    {%{coef: x, exp: drop}, signals}
  end

  def unused(x) do
    y = x * 2
    {y, x}
  end
end
`
			test := "defmodule Shop.SplitTest do\n  use ExUnit.Case\n\n  test \"split\", do: assert(Shop.Split.split(0, 0) == {%{coef: 0, exp: 0}, [:zero]})\nend\n"
			root := newProject(t, shop(map[string]string{"lib/shop/split.ex": source, "test/shop/split_test.exs": test}))
			used := mutantOf(t, root, "lib/shop/split.ex", "exp: drop}", "exp: 0}")
			unused := mutantOf(t, root, "lib/shop/split.ex", "{y, x}", "{x, y}")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{used, unused}, diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{unused.ID: ""}, uncovered)
		})

		t.Run("a mutant of a function that the build leaves out, such as in an if outside a function, is NOT COVERED", func(t *testing.T) {
			source := `defmodule Shop.Pick do
  if Code.ensure_loaded?(Shop.Missing) do
    def pick(%{kind: :missing} = x), do: {x, :missing}
  end

  def pick(x), do: {x, :present}
end
`
			test := "defmodule Shop.PickTest do\n  use ExUnit.Case\n\n  test \"pick\", do: assert(Shop.Pick.pick(1) == {1, :present})\nend\n"
			root := newProject(t, shop(map[string]string{"lib/shop/pick.ex": source, "test/shop/pick_test.exs": test}))
			left := mutantOf(t, root, "lib/shop/pick.ex", "{x, :missing}", "nil")
			leftHead := mutantOf(t, root, "lib/shop/pick.ex", "kind: :missing", "")
			built := mutantOf(t, root, "lib/shop/pick.ex", "{x, :present}", "nil")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{left, leftHead, built}, diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{left.ID: "", leftHead.ID: ""}, uncovered)
		})

		t.Run("a mutant of a module that no test calls is NOT COVERED, with the file in the detail", func(t *testing.T) {
			root := newProject(t, shop(map[string]string{"lib/shop/report.ex": "defmodule Shop.Report do\n  def total(xs), do: Enum.sum(xs) + 1\nend\n"}))
			m := mutantOf(t, root, "lib/shop/report.ex", "Enum.sum(xs) + 1", "Enum.sum(xs) - 1")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m}, diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{m.ID: "no test calls the code of lib/shop/report.ex"}, uncovered)
		})

		t.Run("a mutant of a file that the tests do not compile is NOT COVERED, with the project and the file in the detail", func(t *testing.T) {
			root := newProject(t, shop(map[string]string{"priv/templates/task.ex": "defmodule Shop.Task do\n  def run, do: 1\nend\n"}))
			m := mutantOf(t, root, "priv/templates/task.ex", "1", "2")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m}, diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{m.ID: "the tests of the Elixir project in . do not compile priv/templates/task.ex"}, uncovered)
		})

		t.Run("a mutant of a project with no tests is NOT COVERED, with the project in the detail", func(t *testing.T) {
			root := newProject(t, map[string]string{"services/api/mix.exs": mixProject, "services/api/lib/api.ex": "defmodule Api do\n  def one, do: 1\nend\n"})
			m := mutantOf(t, root, "services/api/lib/api.ex", "1", "2")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m}, diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{m.ID: "the Elixir project in services/api has no tests"}, uncovered)
		})

		t.Run("a mutant of a file in no Mix project is NOT COVERED, with the file in the detail", func(t *testing.T) {
			root := newProject(t, map[string]string{"tools/gen.ex": "defmodule Gen do\n  def one, do: 1\nend\n"})
			m := mutantOf(t, root, "tools/gen.ex", "1", "2")

			uncovered, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m}, diff.Lines{})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{m.ID: "no Mix project holds tools/gen.ex"}, uncovered)
		})

		t.Run("tests that fail with the real code return an error that names the project of the file", func(t *testing.T) {
			root := newProject(t, map[string]string{
				"services/api/mix.exs":              mixProject,
				"services/api/lib/api.ex":           "defmodule Api do\n  def one, do: 1\nend\n",
				"services/api/test/test_helper.exs": "ExUnit.start()\n",
				"services/api/test/api_test.exs":    "defmodule ApiTest do\n  use ExUnit.Case\n\n  test \"one\", do: assert(Api.one() == 2)\nend\n",
			})
			m := mutantOf(t, root, "services/api/lib/api.ex", "1", "2")

			_, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m}, diff.Lines{})

			require.ErrorContains(t, err, "the tests of the Elixir project in services/api fail with the real code")
			var failing *language.TestsFailWithRealCode
			require.ErrorAs(t, err, &failing)
			require.Contains(t, failing.Output, "test one (ApiTest)")
		})

		t.Run("a project that does not compile returns an error that names the project", func(t *testing.T) {
			root := newProject(t, shop(map[string]string{"lib/shop/broken.ex": "defmodule Shop.Broken do\n  def one, do: two()\nend\n"}))
			m := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total > @limit")

			_, err := newAdapter(root).Uncovered(context.Background(), []mutant.Mutant{m}, diff.Lines{})

			require.ErrorContains(t, err, "the Elixir project in . does not compile")
		})
	})

	t.Run("run", func(t *testing.T) {
		t.Run("a mutant that a test catches is KILLED, with the failed test as the detail", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total > @limit")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Killed, Detail: "test edge (Shop.DiscountTest), test/shop/discount_test.exs:8"}, verdict)
		})

		t.Run("with confirm kills, a mutant whose tests fail and then pass when they run again is UNSTABLE, with the failure as the detail", func(t *testing.T) {
			t.Setenv("MUTANTS_TEST_COUNTER", filepath.Join(t.TempDir(), "counter"))
			root := newProject(t, shop(map[string]string{"test/shop/second_run_test.exs": secondRunFailsTest}))
			m := mutantOf(t, root, "lib/shop/discount.ex", "else\n      0", "else\n      0 * 2")

			verdict, err := elixir.New(root, elixir.Settings{Workers: 2, ConfirmKills: true}).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Unstable, Detail: "a test failed, and passed when the tests ran again: test second run (Shop.SecondRunTest), test/shop/second_run_test.exs:4"}, verdict)
		})

		t.Run("without confirm kills, a mutant whose tests fail one time is KILLED, because the tests do not run again", func(t *testing.T) {
			t.Setenv("MUTANTS_TEST_COUNTER", filepath.Join(t.TempDir(), "counter"))
			root := newProject(t, shop(map[string]string{"test/shop/second_run_test.exs": secondRunFailsTest}))
			m := mutantOf(t, root, "lib/shop/discount.ex", "else\n      0", "else\n      0 * 2")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Killed, Detail: "test second run (Shop.SecondRunTest), test/shop/second_run_test.exs:4"}, verdict)
		})

		t.Run("a mutant that each test passes LIVED", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "lib/shop/discount.ex", "else\n      0", "else\n      0 * 2")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Lived}, verdict)
		})

		t.Run("a mutant that does not compile is NOT VIABLE", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total >= @limit +")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.NotViable, verdict.Status)
			require.Contains(t, verdict.Detail, "the mutant does not compile")
		})

		t.Run("in a reused VM, a mutant that a test catches is KILLED, with the failed test as the detail", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total > @limit")

			verdict, err := elixir.New(root, elixir.Settings{Workers: 2, ReuseVM: true}).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Killed, Detail: "test edge (Shop.DiscountTest), test/shop/discount_test.exs:8"}, verdict)
		})

		t.Run("in a reused VM, a mutant that does not compile is NOT VIABLE, with the error as the detail", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total >= @limit +")

			verdict, err := elixir.New(root, elixir.Settings{Workers: 2, ReuseVM: true}).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.NotViable, verdict.Status)
			require.Contains(t, verdict.Detail, "syntax error")
		})

		t.Run("in a reused VM, the next mutant runs against the real code again, so each mutant gets its own verdict", func(t *testing.T) {
			root := newProject(t, shop(nil))
			runner := elixir.New(root, elixir.Settings{Workers: 1, ReuseVM: true}).Runner()
			caught := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total > @limit")
			lives := mutantOf(t, root, "lib/shop/discount.ex", "else\n      0", "else\n      0 * 2")
			var statuses []mutant.Status

			for _, m := range []mutant.Mutant{caught, lives, caught} {
				verdict, err := runner.Run(context.Background(), m)
				require.NoError(t, err)
				statuses = append(statuses, verdict.Status)
			}

			require.Equal(t, []mutant.Status{mutant.Killed, mutant.Lived, mutant.Killed}, statuses)
		})

		t.Run("in a reused VM, a mutant of the code that starts the application is KILLED by the check in a new VM", func(t *testing.T) {
			root := newProject(t, shop(map[string]string{
				"mix.exs": "defmodule Shop.MixProject do\n  use Mix.Project\n\n  def project, do: [app: :shop, version: \"0.1.0\", deps: []]\n\n" +
					"  def application, do: [mod: {Shop.Application, []}]\nend\n",
				"lib/shop/application.ex": "defmodule Shop.Application do\n  use Application\n\n  def start(_type, _args) do\n" +
					"    Supervisor.start_link([%{id: Shop.Limit, start: {Agent, :start_link, [fn -> 100 end, [name: Shop.Limit]]}}], strategy: :one_for_one)\n  end\nend\n",
				"test/shop/limit_test.exs": "defmodule Shop.LimitTest do\n  use ExUnit.Case\n\n  test \"limit\", do: assert(Agent.get(Shop.Limit, & &1) == 100)\nend\n",
			}))
			m := mutantOf(t, root, "lib/shop/application.ex", "fn -> 100 end", "fn -> 101 end")

			verdict, err := elixir.New(root, elixir.Settings{Workers: 2, ReuseVM: true}).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Killed, verdict.Status, verdict.Detail)
		})

		t.Run("a mutant of the code that starts the application is KILLED, because the mutant loads before the application starts", func(t *testing.T) {
			application := `defmodule Shop.Application do
  use Application

  @limit 100

  def start(_type, _args) do
    children = [%{id: Shop.Limit, start: {Agent, :start_link, [fn -> @limit end, [name: Shop.Limit]]}}]
    Supervisor.start_link(children, strategy: :one_for_one)
  end
end
`
			root := newProject(t, map[string]string{
				"mix.exs":                  strings.Replace(mixProject, "deps: []]\n", "deps: []]\n\n  def application, do: [mod: {Shop.Application, []}]\n", 1),
				"lib/shop/application.ex":  application,
				"test/test_helper.exs":     "ExUnit.start()\n",
				"test/shop/limit_test.exs": "defmodule Shop.LimitTest do\n  use ExUnit.Case\n\n  test \"limit\", do: assert(Agent.get(Shop.Limit, & &1) == 100)\nend\n",
			})
			m := mutantOf(t, root, "lib/shop/application.ex", "@limit 100", "@limit 101")

			verdict, err := newAdapter(root).Runner().Run(context.Background(), m)

			require.NoError(t, err)
			require.Equal(t, mutant.Verdict{Status: mutant.Killed, Detail: "test limit (Shop.LimitTest), test/shop/limit_test.exs:4"}, verdict)
		})

		t.Run("writes no file into the project after the build of the real code", func(t *testing.T) {
			root := newProject(t, shop(nil))
			m := mutantOf(t, root, "lib/shop/discount.ex", "total >= @limit", "total > @limit")
			adapter := newAdapter(root)
			_, err := adapter.Uncovered(context.Background(), []mutant.Mutant{m}, diff.Lines{})
			require.NoError(t, err)
			before := filesIn(t, root)

			_, err = adapter.Runner().Run(context.Background(), m)

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
