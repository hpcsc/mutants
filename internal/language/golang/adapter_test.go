//go:build unit

package golang_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/stretchr/testify/require"
)

var defaultSettings = golang.Settings{BuildLimit: 2 * time.Minute, Workers: 2}

func newModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	files["go.mod"] = "module example.com/fixture\n\ngo 1.22\n"
	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return root
}

func editOf(t *testing.T, root, file, operatorName, original, replacement string) operator.Edit {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(root, file))
	require.NoError(t, err)
	start := strings.Index(string(source), original)
	require.GreaterOrEqual(t, start, 0, "%q is not in %s", original, file)
	return operator.Edit{File: file, Operator: operatorName, Start: start, End: start + len(original), Original: original, Replacement: replacement}
}

func editIn(t *testing.T, root, file, operatorName, context, original, replacement string) operator.Edit {
	t.Helper()
	start := editOf(t, root, file, operatorName, context, "").Start + strings.Index(context, original)
	return operator.Edit{File: file, Operator: operatorName, Start: start, End: start + len(original), Original: original, Replacement: replacement}
}

func mutantOf(t *testing.T, root, file, original, replacement string) mutant.Mutant {
	t.Helper()
	edit := editOf(t, root, file, "TEST", original, replacement)
	source, err := os.ReadFile(filepath.Join(root, file))
	require.NoError(t, err)
	before := string(source[:edit.Start])
	line := strings.Count(before, "\n") + 1
	column := edit.Start - strings.LastIndex(before, "\n")
	return mutant.Mutant{
		ID:          mutant.ID{File: file, Function: "f", Operator: "TEST", Number: edit.Start + 1},
		File:        file,
		Line:        line,
		Column:      column,
		Start:       edit.Start,
		End:         edit.End,
		Operator:    "TEST",
		Original:    original,
		Replacement: replacement,
	}
}

func run(t *testing.T, adapter language.Adapter, m mutant.Mutant) mutant.Verdict {
	t.Helper()
	result, err := adapter.Runner().Run(context.Background(), m)
	require.NoError(t, err)
	return result
}

const maxSource = `package calc

func Max(a, b int) int {
	if a < b {
		return b
	}
	return a
}
`

const maxTest = `package calc

import "testing"

func TestMax(t *testing.T) {
	if Max(1, 2) != 2 {
		t.Fatal("Max(1, 2) is not 2")
	}
}
`

const gateSource = `package gate

type Store interface {
	Has(id string) bool
}

type Checker struct {
	store Store
}

func NewChecker(store Store) *Checker {
	return &Checker{store: store}
}

func (c *Checker) Allow(id string) bool {
	if id == "" {
		return false
	}
	return c.closing(id)
}

func (c *Checker) closing(id string) bool {
	if c.store == nil {
		return true
	}
	return c.store.Has(id)
}
`

const gateTest = `package gate

import "testing"

type store map[string]bool

func (s store) Has(id string) bool { return s[id] }

func TestAllow(t *testing.T) {
	if NewChecker(store{"a": true}).Allow("") || !NewChecker(store{"a": true}).Allow("a") || !NewChecker(nil).Allow("b") {
		t.Fatal("Allow")
	}
}
`

const handlerSource = `package handler

import "example.com/fixture/gate"

type Allower interface {
	Allow(id string) bool
}

type Handler struct {
	allow Allower
}

func New(allow Allower) *Handler {
	return &Handler{allow: allow}
}

func Main() *Handler {
	return New(gate.NewChecker(nil))
}

func (h *Handler) Handle(id string) string {
	if h.allow.Allow(id) {
		return "ok"
	}
	return "no"
}
`

const handlerTest = `package handler

import "testing"

type always bool

func (a always) Allow(string) bool { return bool(a) }

func TestHandle(t *testing.T) {
	if New(always(true)).Handle("a") != "ok" {
		t.Fatal("Handle")
	}
}
`

// changedFiles marks each line of each file as changed, as for new files.
func changedFiles(t *testing.T, root string, files ...string) diff.Lines {
	t.Helper()
	var changed diff.Lines
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(root, file))
		require.NoError(t, err)
		changed.Add(file, 1, strings.Count(string(content), "\n"))
	}
	return changed
}

func TestAdapter(t *testing.T) {
	t.Run("keep", func(t *testing.T) {
		t.Run("drops an edit that changes nothing", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource})

			keep := golang.New(root, defaultSettings).Keep(editOf(t, root, "calc/calc.go", "STATEMENT_REMOVE", "a < b", "a < b"))

			require.False(t, keep)
		})

		t.Run("drops an edit in a test file", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})

			keep := golang.New(root, defaultSettings).Keep(editOf(t, root, "calc/calc_test.go", "CONDITIONALS_NEGATION", "!= 2", "== 2"))

			require.False(t, keep)
		})

		t.Run("drops an edit in generated code", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": "// Code generated by hand. DO NOT EDIT.\n\n" + maxSource})

			keep := golang.New(root, defaultSettings).Keep(editOf(t, root, "calc/calc.go", "CONDITIONALS_BOUNDARY", "a < b", "a <= b"))

			require.False(t, keep)
		})

		t.Run("keeps an edit in a file with a build tag only when the tags bring the file in", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"calc/calc.go":  maxSource,
				"calc/extra.go": "//go:build extra\n\npackage calc\n\nfunc Min(a, b int) bool { return a < b }\n",
			})
			edit := editOf(t, root, "calc/extra.go", "CONDITIONALS_BOUNDARY", "a < b", "a <= b")

			require.False(t, golang.New(root, defaultSettings).Keep(edit))
			require.True(t, golang.New(root, golang.Settings{Tags: []string{"extra"}, BuildLimit: time.Minute, Workers: 1}).Keep(edit))
		})

		t.Run("keeps a swap of two fields with identical types, and drops a swap of two types", func(t *testing.T) {
			root := newModule(t, map[string]string{"shape/shape.go": `package shape

type Box struct {
	Width, Height int
	Name          string
}

var Default = Box{Width: 1, Height: 2, Name: "box"}
`})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "shape/shape.go", "SWAP_FIELDS", "1, Height: 2", "2, Height: 1")))
			require.False(t, adapter.Keep(editOf(t, root, "shape/shape.go", "SWAP_FIELDS", `2, Name: "box"`, `"box", Name: 2`)))
		})

		t.Run("drops a swap in a table of named values with constant fields, and keeps a swap of values that are not constant", func(t *testing.T) {
			root := newModule(t, map[string]string{"reason/reason.go": `package reason

type Reason struct{ code string }

type Point struct{ X int }

var Reasons = struct{ NoMatch, Late Reason }{
	NoMatch: Reason{"NoMatch"},
	Late:    Reason{code: "Late"},
}

func Span(a, b int) struct{ Start, End Point } {
	return struct{ Start, End Point }{Start: Point{X: a}, End: Point{X: b}}
}
`})
			adapter := golang.New(root, defaultSettings)
			table := "Reason{\"NoMatch\"},\n\tLate:    Reason{code: \"Late\"}"

			require.False(t, adapter.Keep(editOf(t, root, "reason/reason.go", "SWAP_FIELDS", table, "Reason{code: \"Late\"},\n\tLate:    Reason{\"NoMatch\"}")))
			require.True(t, adapter.Keep(editOf(t, root, "reason/reason.go", "SWAP_FIELDS", "Point{X: a}, End: Point{X: b}", "Point{X: b}, End: Point{X: a}")))
		})

		t.Run("drops a swap of two values in a map literal", func(t *testing.T) {
			root := newModule(t, map[string]string{"shape/shape.go": "package shape\n\nvar Sizes = map[string]int{\"a\": 1, \"b\": 2}\n"})

			keep := golang.New(root, defaultSettings).Keep(editOf(t, root, "shape/shape.go", "SWAP_FIELDS", `1, "b": 2`, `2, "b": 1`))

			require.False(t, keep)
		})

		t.Run("keeps only the zero value that fits the type of the return slot", func(t *testing.T) {
			root := newModule(t, map[string]string{"store/store.go": `package store

type Item struct{ Name string }

type input struct {
	count   int
	label   string
	ok      bool
	current *Item
	list    []string
	box     Item
}

func Load(in input) (int, string, bool, *Item, []string, Item) {
	return in.count + 1, in.label + "!", !in.ok, in.current, in.list, in.box
}
`})
			adapter := golang.New(root, defaultSettings)
			zeros := map[string]string{"in.count + 1": "0", `in.label + "!"`: `""`, "!in.ok": "false", "in.current": "nil", "in.list": "nil", "in.box": "none"}
			for value, zero := range zeros {
				for _, candidate := range []string{"nil", "0", `""`, "false"} {
					keep := adapter.Keep(editOf(t, root, "store/store.go", "RETURN_ZERO", value, candidate))

					require.Equal(t, candidate == zero, keep, "%s -> %s", value, candidate)
				}
			}
		})

		t.Run("keeps an empty literal in place of a struct literal in a struct slot, and drops it when the literal is empty already", func(t *testing.T) {
			root := newModule(t, map[string]string{"store/store.go": `package store

type Trigger struct{ kind int }

func Note() Trigger {
	return Trigger{kind: 1}
}

func None() Trigger {
	return Trigger{kind: 0}
}

func List() []int {
	return []int{1}
}
`})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "store/store.go", "RETURN_ZERO", "Trigger{kind: 1}", "Trigger{}")))
			require.False(t, adapter.Keep(editOf(t, root, "store/store.go", "RETURN_ZERO", "Trigger{kind: 0}", "Trigger{}")))
			require.False(t, adapter.Keep(editOf(t, root, "store/store.go", "RETURN_ZERO", "[]int{1}", "[]int{}")))
		})

		t.Run("keeps 0 for a float slot, and drops it when the value is zero already", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": "package calc\n\nfunc Rate() float64 {\n\treturn 0.0\n}\n\nfunc Ratio(a, b float64) float64 {\n\treturn a / b\n}\n"})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "calc/calc.go", "RETURN_ZERO", "a / b", "0")))
			require.False(t, adapter.Keep(editOf(t, root, "calc/calc.go", "RETURN_ZERO", "0.0", "0")))
		})

		t.Run("keeps nil for an error slot, and leaves the error slot out of RETURN_ZERO", func(t *testing.T) {
			root := newModule(t, map[string]string{"store/store.go": `package store

import "errors"

type Item struct{}

func Load(item *Item) (*Item, error) {
	return item, errors.New("not found")
}
`})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "store/store.go", "RETURN_ERROR_NIL", `errors.New("not found")`, "nil")))
			require.False(t, adapter.Keep(editOf(t, root, "store/store.go", "RETURN_ZERO", `errors.New("not found")`, "nil")))
			require.False(t, adapter.Keep(editIn(t, root, "store/store.go", "RETURN_ERROR_NIL", "return item,", "item", "nil")))
		})

		t.Run("keeps true for a bool slot only, and drops it when the value is true already", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": `package calc

type Valid bool

func Less(a, b int) (bool, int) {
	return a < b, a
}

func Check() Valid {
	return Valid(false)
}

func Always() bool {
	return true
}
`})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "calc/calc.go", "RETURN_TRUE", "a < b", "true")))
			require.True(t, adapter.Keep(editOf(t, root, "calc/calc.go", "RETURN_TRUE", "Valid(false)", "true")))
			require.False(t, adapter.Keep(editIn(t, root, "calc/calc.go", "RETURN_TRUE", "b, a\n", "a", "true")))
			require.False(t, adapter.Keep(editIn(t, root, "calc/calc.go", "RETURN_TRUE", "return true", "true", "(true)")))
		})

		t.Run("drops a decrement of 0 in an index, a slice bound or a size for make, and keeps other decrements of 0", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": `package calc

func First(xs []int) (int, []int, []int, int) {
	start := 0
	return xs[0], xs[0:], make([]int, 0), start
}
`})
			adapter := golang.New(root, defaultSettings)

			require.False(t, adapter.Keep(editIn(t, root, "calc/calc.go", "INTEGER_DECREMENT", "xs[0]", "0", "(0-1)")))
			require.False(t, adapter.Keep(editIn(t, root, "calc/calc.go", "INTEGER_DECREMENT", "xs[0:]", "0", "(0-1)")))
			require.False(t, adapter.Keep(editIn(t, root, "calc/calc.go", "INTEGER_DECREMENT", "make([]int, 0)", "0", "(0-1)")))
			require.True(t, adapter.Keep(editIn(t, root, "calc/calc.go", "INTEGER_DECREMENT", "start := 0", "0", "(0-1)")))
		})

		t.Run("keeps a TIME_BOUNDARY edit only for a method of time.Time, also through an embedded field", func(t *testing.T) {
			root := newModule(t, map[string]string{"wait/wait.go": `package wait

import "time"

type Window struct{ time.Time }

type Gate struct{}

func (Gate) After(n int) bool { return n > 0 }

func Open(now, deadline time.Time, w Window, g Gate) []bool {
	return []bool{now.After(deadline), !w.Before(deadline), g.After(1)}
}
`})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "wait/wait.go", "TIME_BOUNDARY", "now.After(deadline)", "!now.Before(deadline)")))
			require.True(t, adapter.Keep(editOf(t, root, "wait/wait.go", "TIME_BOUNDARY", "!w.Before(deadline)", "w.After(deadline)")))
			require.False(t, adapter.Keep(editOf(t, root, "wait/wait.go", "TIME_BOUNDARY", "g.After(1)", "!g.Before(1)")))
		})

		t.Run("keeps a CALENDAR_DAY edit only for a method of time.Time in a file that imports time by that name", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"due/due.go": `package due

import "time"

type Calendar struct{}

func (Calendar) AddDate(years, months, days int) int { return days }

func Due(start time.Time, c Calendar, n int) (time.Time, int) {
	return start.AddDate(0, 0, n), c.AddDate(0, 0, n)
}
`,
				"due/later.go": `package due

import clock "time"

func Later(start clock.Time) clock.Time {
	return start.AddDate(0, 0, 1)
}
`,
			})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "due/due.go", "CALENDAR_DAY", "start.AddDate(0, 0, n)", "start.Add(time.Duration(n) * 24 * time.Hour)")))
			require.False(t, adapter.Keep(editOf(t, root, "due/due.go", "CALENDAR_DAY", "c.AddDate(0, 0, n)", "c.Add(time.Duration(n) * 24 * time.Hour)")))
			require.False(t, adapter.Keep(editOf(t, root, "due/later.go", "CALENDAR_DAY", "start.AddDate(0, 0, 1)", "start.Add(time.Duration(1) * 24 * time.Hour)")))
		})

		t.Run("drops a FIELD_ZERO edit of a call of a function that the settings name as a zero function", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"maybe/maybe.go": `package maybe

type Maybe[T any] struct {
	value T
	ok    bool
}

func None[T any]() Maybe[T] { return Maybe[T]{} }

func Some[T any](value T) Maybe[T] { return Maybe[T]{value: value, ok: true} }
`,
				"trigger/trigger.go": "package trigger\n\ntype Trigger struct{ kind int }\n\nfunc Submitted() Trigger { return Trigger{} }\n",
				"cases/cases.go": `package cases

import (
	"example.com/fixture/maybe"
	"example.com/fixture/trigger"
)

type Case struct {
	Parker  maybe.Maybe[string]
	Owner   maybe.Maybe[string]
	Trigger trigger.Trigger
}

func New(owner string) Case {
	return Case{Parker: maybe.None[string](), Owner: maybe.Some(owner), Trigger: trigger.Submitted()}
}
`,
			})
			settings := golang.Settings{BuildLimit: time.Minute, Workers: 1, ZeroFunctions: []string{"maybe.None", "trigger.Submitted"}}
			adapter := golang.New(root, settings)
			parker := editOf(t, root, "cases/cases.go", "FIELD_ZERO", "Parker: maybe.None[string](),", "")

			require.False(t, adapter.Keep(parker))
			require.False(t, adapter.Keep(editOf(t, root, "cases/cases.go", "FIELD_ZERO", "Trigger: trigger.Submitted()", "")))
			require.True(t, adapter.Keep(editOf(t, root, "cases/cases.go", "FIELD_ZERO", "Owner: maybe.Some(owner),", "")))
			require.True(t, golang.New(root, defaultSettings).Keep(parker))
		})

		t.Run("keeps a FIELD_ZERO edit in a struct literal only, and drops it when the value is zero already", func(t *testing.T) {
			root := newModule(t, map[string]string{"info/info.go": `package info

type Point struct{ X int }

type Info struct {
	Arrived bool
	ID      string
	Inner   Point
}

func Build(arrived bool, id string) (*Info, map[string]int, Info) {
	return &Info{Arrived: arrived, ID: id}, map[string]int{"a": 1}, Info{Arrived: false, Inner: Point{}}
}
`})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "info/info.go", "FIELD_ZERO", "Arrived: arrived,", "")))
			require.True(t, adapter.Keep(editOf(t, root, "info/info.go", "FIELD_ZERO", "ID: id", "")))
			require.False(t, adapter.Keep(editOf(t, root, "info/info.go", "FIELD_ZERO", `"a": 1`, "")))
			require.False(t, adapter.Keep(editOf(t, root, "info/info.go", "FIELD_ZERO", "Arrived: false,", "")))
			require.False(t, adapter.Keep(editOf(t, root, "info/info.go", "FIELD_ZERO", "Inner: Point{}", "")))
		})

		t.Run("keeps only the zero value that fits the parameter of an argument", func(t *testing.T) {
			root := newModule(t, map[string]string{"client/client.go": `package client

type Point struct{ X int }

type ID int

func (i ID) String() string { return "id" }

func resolve(client string, p Point, limit int, done func()) string { return client }

func Run(id ID) string {
	return resolve(id.String(), Point{X: 1}, 3, func() {})
}
`})
			adapter := golang.New(root, defaultSettings)
			zeros := map[string]string{"id.String()": `""`, "Point{X: 1}": "Point{}", "3": "0", "func() {}": "nil"}
			for value, zero := range zeros {
				for _, candidate := range []string{"nil", "0", `""`, "false", "Point{}"} {
					keep := adapter.Keep(editIn(t, root, "client/client.go", "ARGUMENT_ZERO", "resolve(id.String(), Point{X: 1}, 3, func() {})", value, candidate))

					require.Equal(t, candidate == zero, keep, "%s -> %s", value, candidate)
				}
			}
		})

		t.Run("drops an ARGUMENT_ZERO edit of a builtin, a conversion or a variadic parameter, and keeps the fixed parameters of a variadic call", func(t *testing.T) {
			root := newModule(t, map[string]string{"client/client.go": `package client

import "fmt"

func Run(xs []int, id int) (int, int64, string) {
	return len(xs), int64(id), fmt.Sprintf("%d", id)
}
`})
			adapter := golang.New(root, defaultSettings)

			require.False(t, adapter.Keep(editIn(t, root, "client/client.go", "ARGUMENT_ZERO", "len(xs)", "xs", "nil")))
			require.False(t, adapter.Keep(editIn(t, root, "client/client.go", "ARGUMENT_ZERO", "int64(id)", "id", "0")))
			require.False(t, adapter.Keep(editIn(t, root, "client/client.go", "ARGUMENT_ZERO", `"%d", id)`, "id", "0")))
			require.True(t, adapter.Keep(editOf(t, root, "client/client.go", "ARGUMENT_ZERO", `"%d"`, `""`)))
		})

		t.Run("drops an ARGUMENT_ZERO edit of a context and of the text of an error, and keeps the other arguments", func(t *testing.T) {
			root := newModule(t, map[string]string{"client/client.go": `package client

import (
	"context"
	"errors"
	"fmt"
)

func load(ctx context.Context, id string) error { return nil }

func validate(name string) error { return nil }

func Run(ctx context.Context, id, name string, cause error) []error {
	return []error{load(ctx, id), load(context.Background(), id), errors.New("not found"), fmt.Errorf("load %s: %w", id, cause), validate(name)}
}
`})
			adapter := golang.New(root, defaultSettings)

			require.False(t, adapter.Keep(editIn(t, root, "client/client.go", "ARGUMENT_ZERO", "load(ctx, id)", "ctx", "nil")))
			require.False(t, adapter.Keep(editOf(t, root, "client/client.go", "ARGUMENT_ZERO", "context.Background()", "nil")))
			require.False(t, adapter.Keep(editOf(t, root, "client/client.go", "ARGUMENT_ZERO", `"not found"`, `""`)))
			require.False(t, adapter.Keep(editOf(t, root, "client/client.go", "ARGUMENT_ZERO", `"load %s: %w"`, `""`)))
			require.True(t, adapter.Keep(editIn(t, root, "client/client.go", "ARGUMENT_ZERO", "load(ctx, id)", "id", `""`)))
			require.True(t, adapter.Keep(editIn(t, root, "client/client.go", "ARGUMENT_ZERO", "validate(name)", "name", `""`)))
		})

		t.Run("finds the slot of a return in a function literal inside a function", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": "package calc\n\nimport \"strconv\"\n\nfunc Outer() string {\n\tf := func(n int) int { return n + 1 }\n\treturn strconv.Itoa(f(1))\n}\n"})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "calc/calc.go", "RETURN_ZERO", "n + 1", "0")))
			require.False(t, adapter.Keep(editOf(t, root, "calc/calc.go", "RETURN_ZERO", "n + 1", `""`)))
		})

		t.Run("keeps a FIELD_ZERO and a SWAP_FIELDS edit in a literal of a slice of pointers", func(t *testing.T) {
			root := newModule(t, map[string]string{"info/info.go": "package info\n\ntype Info struct {\n\tArrived bool\n\tID      string\n\tName    string\n}\n\nvar All = []*Info{{Arrived: true, ID: \"a\", Name: \"b\"}}\n"})
			adapter := golang.New(root, defaultSettings)

			require.True(t, adapter.Keep(editOf(t, root, "info/info.go", "FIELD_ZERO", "Arrived: true,", "")))
			require.True(t, adapter.Keep(editOf(t, root, "info/info.go", "SWAP_FIELDS", `"a", Name: "b"`, `"b", Name: "a"`)))
		})

		t.Run("drops each RETURN_ZERO edit for a slot whose type is a type parameter", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": "package calc\n\nfunc First[T any](xs []T) T {\n\treturn xs[0]\n}\n"})
			adapter := golang.New(root, defaultSettings)

			for _, candidate := range []string{"nil", "0", `""`, "false"} {
				require.False(t, adapter.Keep(editOf(t, root, "calc/calc.go", "RETURN_ZERO", "xs[0]", candidate)), candidate)
			}
		})

		t.Run("finds the slot of a return in a function literal", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": "package calc\n\nvar Next = func(n int) (int, error) {\n\treturn n + 1, nil\n}\n"})

			keep := golang.New(root, defaultSettings).Keep(editOf(t, root, "calc/calc.go", "RETURN_ZERO", "n + 1", "0"))

			require.True(t, keep)
		})
	})

	t.Run("function", func(t *testing.T) {
		t.Run("names a method by its receiver and a function by its name", func(t *testing.T) {
			root := newModule(t, map[string]string{"order/handler.go": `package order

type Handler struct{}

func (h *Handler) accounts() int { return 1 }

func (h Handler) name() int { return 2 }

type List[T any] struct{}

func (l *List[T]) Push() int { return 3 }

func total() int { return 4 }

var limit = 5

type Pair[K comparable, V any] struct{}

func (p *Pair[K, V]) Get() int { return 6 }
`})
			adapter := golang.New(root, defaultSettings)
			names := map[string]string{"1": "(*Handler).accounts", "2": "Handler.name", "3": "(*List).Push", "4": "total", "5": "limit", "6": "(*Pair).Get"}
			for value, name := range names {
				edit := editOf(t, root, "order/handler.go", "INTEGER_INCREMENT", value, "")

				require.Equal(t, name, adapter.Function("order/handler.go", edit.Start), value)
			}
		})
	})

	t.Run("uncovered", func(t *testing.T) {
		t.Run("marks a mutant inside a branch that no test enters, and runs the mutant on the line of its if", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"calc/calc.go":      "package calc\n\nimport \"errors\"\n\nfunc Check(n int) error {\n\tif n < 0 {\n\t\treturn errors.New(\"negative\")\n\t}\n\treturn nil\n}\n",
				"calc/calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestCheck(t *testing.T) {\n\tif Check(1) != nil {\n\t\tt.Fatal(\"1 is not negative\")\n\t}\n}\n",
			})
			branch := mutantOf(t, root, "calc/calc.go", "{\n\t\treturn errors.New(\"negative\")\n\t}", "{}")
			value := mutantOf(t, root, "calc/calc.go", `errors.New("negative")`, "nil")

			uncovered, err := golang.New(root, defaultSettings).Uncovered(context.Background(), []mutant.Mutant{branch, value})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{value.ID: ""}, uncovered)
		})

		t.Run("runs a mutant after a function literal that no test calls", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"calc/calc.go": `package calc

func Sum(xs []int, offset int) int {
	return apply(xs, func(v int) int {
		return v * 2
	}, offset+1)
}

func apply(xs []int, double func(int) int, extra int) int {
	for _, x := range xs {
		extra += double(x)
	}
	return extra
}
`,
				"calc/calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) {\n\tif Sum(nil, 1) != 2 {\n\t\tt.Fatal(\"Sum(nil, 1) is not 2\")\n\t}\n}\n",
			})
			afterLiteral := mutantOf(t, root, "calc/calc.go", "offset+1", "offset-1")
			insideLiteral := mutantOf(t, root, "calc/calc.go", "v * 2", "v / 2")

			uncovered, err := golang.New(root, defaultSettings).Uncovered(context.Background(), []mutant.Mutant{afterLiteral, insideLiteral})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{insideLiteral.ID: ""}, uncovered)
		})

		t.Run("marks every mutant of a package with no test files, with the reason", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{"calc/calc.go": maxSource})
			m := mutantOf(t, root, "calc/calc.go", "a < b", "a <= b")

			uncovered, err := golang.New(root, defaultSettings).Uncovered(context.Background(), []mutant.Mutant{m})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{m.ID: "package calc has no test files"}, uncovered)
		})

		t.Run("a package whose tests fail with the real code returns an error that names it", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"calc/calc.go":      maxSource,
				"calc/calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestMax(t *testing.T) {\n\tt.Fatal(\"red\")\n}\n",
			})

			_, err := golang.New(root, defaultSettings).Uncovered(context.Background(), []mutant.Mutant{mutantOf(t, root, "calc/calc.go", "a < b", "a <= b")})

			require.ErrorContains(t, err, "the tests of example.com/fixture/calc fail with the real code")
		})

		t.Run("reads the coverage of a folder whose name differs from its package name", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"handlers/check.go":      "package calc\n\nimport \"errors\"\n\nfunc Check(n int) error {\n\tif n < 0 {\n\t\treturn errors.New(\"negative\")\n\t}\n\treturn nil\n}\n",
				"handlers/check_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestCheck(t *testing.T) {\n\tif Check(1) != nil {\n\t\tt.Fatal(\"1 is not negative\")\n\t}\n}\n",
			})
			value := mutantOf(t, root, "handlers/check.go", `errors.New("negative")`, "nil")

			uncovered, err := golang.New(root, defaultSettings).Uncovered(context.Background(), []mutant.Mutant{value})

			require.NoError(t, err)
			require.Equal(t, map[mutant.ID]string{value.ID: ""}, uncovered)
		})

		t.Run("with the tags, a test file with a build tag runs", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": "//go:build unit\n\n" + maxTest})
			m := mutantOf(t, root, "calc/calc.go", "return b", "return a")

			withoutTags, err := golang.New(root, defaultSettings).Uncovered(context.Background(), []mutant.Mutant{m})
			require.NoError(t, err)
			withTags, err := golang.New(root, golang.Settings{Tags: []string{"unit"}, BuildLimit: time.Minute, Workers: 1}).Uncovered(context.Background(), []mutant.Mutant{m})
			require.NoError(t, err)

			require.Equal(t, map[mutant.ID]string{m.ID: "package calc has no test files"}, withoutTags)
			require.Empty(t, withTags)
		})
	})

	t.Run("caller gaps", func(t *testing.T) {
		t.Run("gives the changed statements that the tests of the package run, but that no test of a changed caller with a fake runs", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"gate/gate.go": gateSource, "gate/gate_test.go": gateTest,
				"handler/handler.go": handlerSource, "handler/handler_test.go": handlerTest,
			})

			gaps, err := golang.New(root, defaultSettings).CallerGaps(context.Background(), changedFiles(t, root, "gate/gate.go", "handler/handler.go"))

			require.NoError(t, err)
			require.Equal(t, []language.CallerGap{
				{File: "gate/gate.go", Function: "NewChecker", Lines: []int{12}, Callers: []string{"handler"}},
				{File: "gate/gate.go", Function: "(*Checker).Allow", Lines: []int{16, 17, 19}, Callers: []string{"handler"}},
				{File: "gate/gate.go", Function: "(*Checker).closing", Lines: []int{23, 24, 26}, Callers: []string{"handler"}},
			}, gaps)
		})

		t.Run("gives only the statements that the tests of the caller do not run, when they run the real package", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"gate/gate.go": gateSource, "gate/gate_test.go": gateTest,
				"wired/wired.go":      "package wired\n\nimport \"example.com/fixture/gate\"\n\nfunc Allowed(id string) bool {\n\treturn gate.NewChecker(nil).Allow(id)\n}\n",
				"wired/wired_test.go": "package wired\n\nimport \"testing\"\n\nfunc TestAllowed(t *testing.T) {\n\tif !Allowed(\"a\") {\n\t\tt.Fatal(\"Allowed\")\n\t}\n}\n",
			})

			gaps, err := golang.New(root, defaultSettings).CallerGaps(context.Background(), changedFiles(t, root, "gate/gate.go", "wired/wired.go"))

			require.NoError(t, err)
			require.Equal(t, []language.CallerGap{
				{File: "gate/gate.go", Function: "(*Checker).Allow", Lines: []int{17}, Callers: []string{"wired"}},
				{File: "gate/gate.go", Function: "(*Checker).closing", Lines: []int{26}, Callers: []string{"wired"}},
			}, gaps)
		})

		t.Run("gives only the changed statements of the package, not its other statements", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"gate/gate.go": gateSource, "gate/gate_test.go": gateTest,
				"handler/handler.go": handlerSource, "handler/handler_test.go": handlerTest,
			})
			changed := changedFiles(t, root, "handler/handler.go")
			changed.Add("gate/gate.go", 15, 20)

			gaps, err := golang.New(root, defaultSettings).CallerGaps(context.Background(), changed)

			require.NoError(t, err)
			require.Equal(t, []language.CallerGap{
				{File: "gate/gate.go", Function: "(*Checker).Allow", Lines: []int{16, 17, 19}, Callers: []string{"handler"}},
			}, gaps)
		})

		t.Run("follows the functions that a reached function calls", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"gate/gate.go":            "package gate\n\nfunc Allow(id string) bool {\n\treturn valid(id)\n}\n\nfunc valid(id string) bool {\n\treturn id != \"\"\n}\n",
				"gate/gate_test.go":       "package gate\n\nimport \"testing\"\n\nfunc TestAllow(t *testing.T) {\n\tif !Allow(\"a\") {\n\t\tt.Fatal(\"Allow\")\n\t}\n}\n",
				"handler/handler.go":      "package handler\n\nimport \"example.com/fixture/gate\"\n\nvar allow = gate.Allow\n\nfunc Handle(id string, check func(string) bool) bool {\n\treturn check(id)\n}\n\nfunc Main(id string) bool {\n\treturn Handle(id, allow)\n}\n",
				"handler/handler_test.go": "package handler\n\nimport \"testing\"\n\nfunc TestHandle(t *testing.T) {\n\tif !Handle(\"a\", func(string) bool { return true }) {\n\t\tt.Fatal(\"Handle\")\n\t}\n}\n",
			})

			gaps, err := golang.New(root, defaultSettings).CallerGaps(context.Background(), changedFiles(t, root, "gate/gate.go", "handler/handler.go"))

			require.NoError(t, err)
			require.Equal(t, []language.CallerGap{
				{File: "gate/gate.go", Function: "Allow", Lines: []int{4}, Callers: []string{"handler"}},
				{File: "gate/gate.go", Function: "valid", Lines: []int{8}, Callers: []string{"handler"}},
			}, gaps)
		})

		t.Run("gives no gap when the changed lines of the caller call only an interface of its own", func(t *testing.T) {
			t.Parallel()
			root := newModule(t, map[string]string{
				"gate/gate.go": gateSource, "gate/gate_test.go": gateTest,
				"handler/handler.go": handlerSource, "handler/handler_test.go": handlerTest,
			})
			changed := changedFiles(t, root, "gate/gate.go")
			changed.Add("handler/handler.go", 21, 26)

			gaps, err := golang.New(root, defaultSettings).CallerGaps(context.Background(), changed)

			require.NoError(t, err)
			require.Empty(t, gaps)
		})
	})

	t.Run("run", func(t *testing.T) {
		t.Run("a mutant that a test catches is KILLED", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", "return b", "return a"))

			require.Equal(t, mutant.Killed, result.Status, result.Detail)
			require.Contains(t, result.Detail, "--- FAIL: TestMax")
		})

		t.Run("a mutant that every test passes is LIVED", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", "a < b", "a <= b"))

			require.Equal(t, mutant.Lived, result.Status, result.Detail)
		})

		t.Run("a mutant that does not build is NOT VIABLE", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", "return b", `return "b"`))

			require.Equal(t, mutant.NotViable, result.Status, result.Detail)
			require.Contains(t, result.Detail, "calc.go")
		})

		t.Run("builds a mutant through the build cache of the settings, with a mutant cache that the run deletes", func(t *testing.T) {
			executable, err := os.Executable()
			require.NoError(t, err)
			log := filepath.Join(t.TempDir(), "log")
			t.Setenv(buildCacheLog, log)
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})
			settings := defaultSettings
			settings.CacheProgram = []string{executable}

			result := run(t, golang.New(root, settings), mutantOf(t, root, "calc/calc.go", "return b", "return a"))

			require.Equal(t, mutant.Killed, result.Status, result.Detail)
			served, err := os.ReadFile(log)
			require.NoError(t, err)
			mutantCaches := strings.Fields(string(served))
			require.Len(t, mutantCaches, 1)
			require.NoDirExists(t, mutantCaches[0])
		})

		t.Run("a build cache that does not start gives INFRA ERROR, not NOT VIABLE", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})
			settings := defaultSettings
			settings.CacheProgram = []string{filepath.Join(t.TempDir(), "missing")}

			result := run(t, golang.New(root, settings), mutantOf(t, root, "calc/calc.go", "return b", "return a"))

			require.Equal(t, mutant.InfraError, result.Status, result.Detail)
			require.Contains(t, result.Detail, "GOCACHEPROG")
		})

		t.Run("keeps the GOCACHEPROG of the user in place of the build cache of the settings", func(t *testing.T) {
			executable, err := os.Executable()
			require.NoError(t, err)
			userCache, err := exec.Command("go", "env", "GOCACHE").Output()
			require.NoError(t, err)
			log := filepath.Join(t.TempDir(), "log")
			t.Setenv(buildCacheLog, log)
			t.Setenv("GOCACHEPROG", fmt.Sprintf("'%s' '%[2]s' '%[2]s'", executable, strings.TrimSpace(string(userCache))))
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})
			settings := defaultSettings
			settings.CacheProgram = []string{filepath.Join(t.TempDir(), "missing")}

			result := run(t, golang.New(root, settings), mutantOf(t, root, "calc/calc.go", "return b", "return a"))

			require.Equal(t, mutant.Killed, result.Status, result.Detail)
			served, err := os.ReadFile(log)
			require.NoError(t, err)
			require.Contains(t, string(served), strings.TrimSpace(string(userCache)))
		})

		t.Run("a mutant that removes the only use of an import still builds", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"calc/calc.go": `package calc

import (
	"errors"
	format "fmt"
)

func Check(n int) error {
	if n < 0 {
		return format.Errorf("check %d: %w", n, errors.New("negative"))
	}
	return nil
}
`,
				"calc/calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestCheck(t *testing.T) {\n\tif Check(1) != nil {\n\t\tt.Fatal(\"1 is not negative\")\n\t}\n}\n",
			})
			branch := "{\n\t\treturn format.Errorf(\"check %d: %w\", n, errors.New(\"negative\"))\n\t}"

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", branch, "{}"))

			require.Equal(t, mutant.Lived, result.Status, result.Detail)
		})

		t.Run("a mutant that removes the only use of a variable still builds", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"calc/calc.go": `package calc

import (
	"slices"
	"strconv"
)

func Allowed(items []string, id string) bool {
	requested := items
	if id == "" || !slices.Contains(requested, id) {
		return false
	}
	return true
}

func Over(limits map[string]int, id string, n int) bool {
	if limit, found := limits[id]; found && n > limit {
		return true
	}
	return false
}

func Name(v any) string {
	switch value := v.(type) {
	case int:
		return strconv.Itoa(value)
	}
	return ""
}
`,
				"calc/calc_test.go": `package calc

import "testing"

func TestCalc(t *testing.T) {
	if !Allowed([]string{"a"}, "a") || Allowed(nil, "") {
		t.Fatal("Allowed")
	}
	if !Over(map[string]int{"a": 1}, "a", 2) || Over(map[string]int{"a": 1}, "a", 0) {
		t.Fatal("Over")
	}
	if Name("a") != "" {
		t.Fatal("Name")
	}
}
`,
			})
			adapter := golang.New(root, defaultSettings)
			mutants := map[string]string{
				`id == "" || !slices.Contains(requested, id)`: `id == ""`,
				"found && n > limit":                          "n > limit",
				"strconv.Itoa(value)":                         `"int"`,
			}

			for original, replacement := range mutants {
				result := run(t, adapter, mutantOf(t, root, "calc/calc.go", original, replacement))

				require.Equal(t, mutant.Lived, result.Status, "%s -> %s: %s", original, replacement, result.Detail)
			}
		})

		t.Run("a mutant build gets three times the build of the real code when that is longer than the build limit", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})
			settings := golang.Settings{BuildLimit: time.Millisecond, Workers: 1}

			result := run(t, golang.New(root, settings), mutantOf(t, root, "calc/calc.go", "return b", "return a"))

			require.Equal(t, mutant.Killed, result.Status, result.Detail)
		})

		t.Run("a mutant of a file that changed after mutants read it is INFRA ERROR", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})
			m := mutantOf(t, root, "calc/calc.go", "return b", "return a")
			require.NoError(t, os.WriteFile(filepath.Join(root, "calc/calc.go"), []byte(strings.Replace(maxSource, "return b", "return  b", 1)), 0o644))

			result := run(t, golang.New(root, defaultSettings), m)

			require.Equal(t, mutant.InfraError, result.Status, result.Detail)
			require.Contains(t, result.Detail, "changed after mutants read it")
		})

		t.Run("a mutant whose tests a signal stops is INFRA ERROR, not KILLED", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"calc/calc.go":      "package calc\n\nfunc Stop() bool {\n\treturn false\n}\n",
				"calc/calc_test.go": "package calc\n\nimport (\n\t\"os\"\n\t\"syscall\"\n\t\"testing\"\n)\n\nfunc TestStop(t *testing.T) {\n\tif Stop() {\n\t\t_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)\n\t}\n}\n",
			})

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", "false", "true"))

			require.Equal(t, mutant.InfraError, result.Status, result.Detail)
		})

		t.Run("a mutant that makes a test panic is KILLED", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"calc/calc.go":      "package calc\n\nfunc First(xs []int) int {\n\tif len(xs) == 0 {\n\t\treturn 0\n\t}\n\treturn xs[0]\n}\n",
				"calc/calc_test.go": "package calc\n\nimport \"testing\"\n\nfunc TestFirst(t *testing.T) {\n\tif First(nil) != 0 {\n\t\tt.Fatal(\"First(nil) is not 0\")\n\t}\n}\n",
			})

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", "len(xs) == 0", "len(xs) != 0"))

			require.Equal(t, mutant.Killed, result.Status, result.Detail)
			require.Contains(t, result.Detail, "panic:")
		})

		t.Run("a mutant that runs past the limit two times is TIMED OUT, and no process of its tests stays alive", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"calc/calc.go": "package calc\n\nfunc Count(n int) int {\n\tc := 0\n\tfor i := 0; i < n; i++ {\n\t\tc++\n\t}\n\treturn c\n}\n",
				"calc/calc_test.go": `package calc

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestCount(t *testing.T) {
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("child.pid", []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if Count(3) != 3 {
		t.Fatal("Count(3) is not 3")
	}
}
`,
			})

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", "i++", "i--"))

			require.Equal(t, mutant.TimedOut, result.Status, result.Detail)
			pid, err := os.ReadFile(filepath.Join(root, "calc", "child.pid"))
			require.NoError(t, err)
			child, err := strconv.Atoi(string(pid))
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				return errors.Is(syscall.Kill(child, 0), syscall.ESRCH)
			}, 5*time.Second, 50*time.Millisecond, "the child of the test binary is alive")
		})

		t.Run("the run leaves the package folder as it was", func(t *testing.T) {
			root := newModule(t, map[string]string{"calc/calc.go": maxSource, "calc/calc_test.go": maxTest})

			run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", "return b", "return a"))

			entries, err := os.ReadDir(filepath.Join(root, "calc"))
			require.NoError(t, err)
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			require.Equal(t, []string{"calc.go", "calc_test.go"}, names)
			source, err := os.ReadFile(filepath.Join(root, "calc", "calc.go"))
			require.NoError(t, err)
			require.Equal(t, maxSource, string(source))
		})

		t.Run("the tests run in the package folder, so they read their testdata", func(t *testing.T) {
			root := newModule(t, map[string]string{
				"calc/calc.go":           "package calc\n\nfunc Greeting() string {\n\treturn \"hello\"\n}\n\nfunc Shout() string {\n\treturn \"HEY\"\n}\n",
				"calc/testdata/greeting": "hello",
				"calc/calc_test.go":      "package calc\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestGreeting(t *testing.T) {\n\twant, err := os.ReadFile(\"testdata/greeting\")\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif Greeting() != string(want) {\n\t\tt.Fatal(\"wrong greeting\")\n\t}\n}\n",
			})

			result := run(t, golang.New(root, defaultSettings), mutantOf(t, root, "calc/calc.go", `"HEY"`, `""`))

			require.Equal(t, mutant.Lived, result.Status, result.Detail)
		})
	})
}
