package elixir

import (
	"context"
	_ "embed"
	"path"
	"slices"
	"strings"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/operator/astgrep"
)

//go:embed mutants_task.exs
var task []byte

const (
	returnEmptyRules      = "RETURN_EMPTY/"
	returnTrueAnnotated   = "RETURN_TRUE/annotated"
	statementRemoveAssign = "STATEMENT_REMOVE/assign"
	namedValueRemove      = "NAMED_VALUE_REMOVE"
)

type Settings struct {
	Workers      int
	ConfirmKills bool
}

type adapter struct {
	sources  *sourceFiles
	structs  *structDefaults
	coverage *coverage
	runner   *runner
}

func New(root string, settings Settings) language.Adapter {
	projects := newProjects(root)
	coverage := newCoverage(settings, projects)
	matcher := astgrep.New(root)
	return &adapter{
		sources:  newSourceFiles(root, matcher),
		structs:  newStructDefaults(root, matcher),
		coverage: coverage,
		runner:   &runner{root: root, projects: projects, coverage: coverage, confirmKills: settings.ConfirmKills},
	}
}

func (a *adapter) Name() string {
	return "elixir"
}

func (a *adapter) Extensions() []string {
	return []string{".ex"}
}

func (a *adapter) IndentationMatters() bool {
	return false
}

// TestFile takes the files of the skip rule test-file in skip/elixir/test_file.yml.
func (a *adapter) TestFile(file string) bool {
	return slices.Contains(strings.Split(path.Dir(file), "/"), "test")
}

func (a *adapter) Keep(candidate operator.Edit) bool {
	if candidate.Replacement == candidate.Original {
		return false
	}
	switch {
	case strings.HasPrefix(candidate.Rule, returnEmptyRules):
		return candidate.Replacement == emptyOf(a.sources.returnType(candidate.File, candidate.Start))
	case candidate.Rule == returnTrueAnnotated:
		return returnsBoolean(a.sources.returnType(candidate.File, candidate.Start))
	case candidate.Rule == statementRemoveAssign:
		return len(a.sources.unbound(candidate.File, candidate.Start, candidate.End)) == 0
	case candidate.Operator == namedValueRemove:
		return !a.removesDefault(candidate)
	}
	return true
}

func (a *adapter) removesDefault(candidate operator.Edit) bool {
	modules, field, found := a.sources.removedStructField(candidate.File, candidate.Start, candidate.End)
	if !found {
		return false
	}
	for _, module := range modules {
		if defaults, known := a.structs.fieldsOf(module); known {
			value, hasDefault := defaults[field.field]
			return hasDefault && value == field.value
		}
	}
	return false
}

func (a *adapter) Function(file string, offset int) string {
	return a.sources.function(file, offset)
}

func (a *adapter) Uncovered(ctx context.Context, mutants []mutant.Mutant, _ diff.Lines) (map[mutant.ID]string, error) {
	return a.coverage.uncovered(ctx, mutants, a.sources)
}

func (a *adapter) Runner() mutant.Runner {
	return a.runner
}

func emptyOf(function, returns string) string {
	compact := strings.Join(strings.Fields(returns), "")
	switch {
	case compact == "" && strings.HasSuffix(function, "?"):
		return "false"
	case compact == "" || strings.Contains("|"+compact+"|", "|nil|"):
		return "nil"
	case strings.HasPrefix(compact, "["):
		return "[]"
	case strings.HasPrefix(compact, "%{"):
		return "%{}"
	}
	switch typeName(compact) {
	case "integer", "non_neg_integer", "pos_integer", "neg_integer", "number", "float", "byte", "char", "arity":
		return "0"
	case "String.t", "binary", "bitstring":
		return `""`
	case "boolean":
		return "false"
	case "list", "keyword":
		return "[]"
	case "map":
		return "%{}"
	}
	return "nil"
}

func returnsBoolean(function, returns string) bool {
	if returns == "" {
		return strings.HasSuffix(function, "?")
	}
	return typeName(strings.Join(strings.Fields(returns), "")) == "boolean"
}

func typeName(annotation string) string {
	name, _, _ := strings.Cut(annotation, "(")
	return name
}

var _ language.Adapter = (*adapter)(nil)

var _ mutant.Runner = (*runner)(nil)
