package python

import (
	"context"
	"embed"
	"strings"

	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/operator/astgrep"
)

//go:embed sitecustomize.py mutants_plugin.py
var support embed.FS

const (
	returnEmptyRules      = "RETURN_EMPTY/"
	returnTrueAnnotated   = "RETURN_TRUE/annotated"
	statementRemoveAssign = "STATEMENT_REMOVE/assign"
)

type Settings struct {
	Command []string
	Workers int
}

type adapter struct {
	sources  *sourceFiles
	coverage *coverage
	runner   *runner
}

func New(root string, settings Settings) language.Adapter {
	projects := newProjects(root, settings.Command)
	coverage := newCoverage(settings, projects)
	return &adapter{
		sources:  newSourceFiles(root, astgrep.New(root)),
		coverage: coverage,
		runner:   &runner{root: root, projects: projects, coverage: coverage},
	}
}

func (a *adapter) Name() string {
	return "python"
}

func (a *adapter) Extensions() []string {
	return []string{".py"}
}

func (a *adapter) Keep(candidate operator.Edit) bool {
	if candidate.Replacement == candidate.Original {
		return false
	}
	switch {
	case strings.HasPrefix(candidate.Rule, returnEmptyRules):
		return candidate.Replacement == emptyOf(a.sources.returnType(candidate.File, candidate.Start))
	case candidate.Rule == returnTrueAnnotated:
		return typeName(a.sources.returnType(candidate.File, candidate.Start)) == "bool"
	case candidate.Rule == statementRemoveAssign:
		return len(a.sources.unbound(candidate.File, candidate.Start, candidate.End)) == 0
	}
	return true
}

func (a *adapter) Function(file string, offset int) string {
	return a.sources.function(file, offset)
}

func (a *adapter) Uncovered(ctx context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error) {
	return a.coverage.uncovered(ctx, mutants)
}

func (a *adapter) Runner() mutant.Runner {
	return a.runner
}

func emptyOf(returns string) string {
	compact := strings.Join(strings.Fields(returns), "")
	if compact == "" || strings.Contains("|"+compact+"|", "|None|") {
		return "None"
	}
	switch typeName(compact) {
	case "int", "float", "complex":
		return "0"
	case "str":
		return `""`
	case "bool":
		return "False"
	case "list", "List", "Sequence", "MutableSequence", "Iterable", "Collection":
		return "[]"
	case "dict", "Dict", "Mapping", "MutableMapping":
		return "{}"
	}
	return "None"
}

func typeName(annotation string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(annotation), "[")
	return name[strings.LastIndex(name, ".")+1:]
}

var _ language.Adapter = (*adapter)(nil)

var _ mutant.Runner = (*runner)(nil)
