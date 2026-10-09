package golang

import (
	"context"
	"go/ast"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
)

const (
	namedValueSwap   = "NAMED_VALUE_SWAP"
	returnEmpty      = "RETURN_EMPTY"
	errorRemove      = "ERROR_REMOVE"
	returnTrue       = "RETURN_TRUE"
	integerDecrement = "INTEGER_DECREMENT"
	namedValueRemove = "NAMED_VALUE_REMOVE"
	argumentEmpty    = "ARGUMENT_EMPTY"
	branchIf         = "BRANCH_IF"
	branchElse       = "BRANCH_ELSE"
	branchCase       = "BRANCH_CASE"

	timeBoundaryRules = "CONDITIONALS_BOUNDARY/time-"
	plusAssignRule    = "INCREMENT_DECREMENT/plus-assign"
)

type Settings struct {
	Tags          []string
	BuildLimit    time.Duration
	Workers       int
	ZeroFunctions []string
	ExcludeTypes  []string
	CacheProgram  []string
}

func (s Settings) tagArguments() []string {
	if len(s.Tags) == 0 {
		return nil
	}
	return []string{"-tags=" + strings.Join(s.Tags, ",")}
}

func (s Settings) buildEnv() []string {
	if os.Getenv("GOFLAGS") != "" {
		return os.Environ()
	}
	return append(os.Environ(), "GOFLAGS=-p=2")
}

func (s Settings) testEnv() []string {
	return append(os.Environ(), "GOMAXPROCS="+strconv.Itoa(max(1, runtime.NumCPU()/max(1, s.Workers))))
}

type adapter struct {
	root         string
	excludeTypes []string
	finder       *packageFinder
	sources      *sourceFiles
	types        *typeChecker
	coverage     *coverage
	callerGaps   *callerGaps
	runner       *runner
}

func New(root string, settings Settings) language.Adapter {
	finder := newPackageFinder(settings.tagArguments())
	sources := newSourceFiles(root)
	types := newTypeChecker(settings.tagArguments(), settings.ZeroFunctions)
	coverage := newCoverage(root, settings, finder, types, sources)
	return &adapter{
		root:         root,
		excludeTypes: settings.ExcludeTypes,
		finder:       finder,
		sources:      sources,
		types:        types,
		coverage:     coverage,
		callerGaps:   &callerGaps{root: root, settings: settings, coverage: coverage, types: types, sources: sources},
		runner:       &runner{root: root, settings: settings, finder: finder, coverage: coverage, userCache: sync.OnceValue(func() string { return userCache(root, settings.CacheProgram) })},
	}
}

func (a *adapter) Name() string {
	return "go"
}

func (a *adapter) Extensions() []string {
	return []string{".go"}
}

func (a *adapter) IndentationMatters() bool {
	return false
}

func (a *adapter) Keep(candidate operator.Edit) bool {
	if candidate.Replacement == candidate.Original || strings.HasSuffix(candidate.File, "_test.go") {
		return false
	}
	syntax, _, err := a.sources.parse(candidate.File)
	if err != nil || ast.IsGenerated(syntax) || a.sources.belongsTo(candidate.File, candidate.Start, a.excludeTypes) {
		return false
	}
	path := filepath.Join(a.root, candidate.File)
	pkg, err := a.finder.find(context.Background(), filepath.Dir(path))
	if err != nil || !pkg.builds(filepath.Base(path)) {
		return false
	}
	if strings.HasPrefix(candidate.Rule, timeBoundaryRules) {
		return a.types.callsTimeMethod(path, candidate.Start, candidate.End)
	}
	if candidate.Rule == plusAssignRule {
		return a.types.addsNumbers(path, candidate.Start, candidate.End)
	}
	switch candidate.Operator {
	case namedValueSwap:
		return a.types.canSwap(path, candidate.Start, candidate.End)
	case returnEmpty:
		zero := a.types.zeroOfSlot(path, candidate.Start, candidate.End)
		return zero != "" && zero == candidate.Replacement
	case errorRemove:
		return a.types.isErrorSlot(path, candidate.Start, candidate.End)
	case returnTrue:
		return a.types.canBecomeTrue(path, candidate.Start, candidate.End)
	case integerDecrement:
		return !a.sources.isZeroIndexOrSize(candidate.File, candidate.Start, candidate.End)
	case namedValueRemove:
		return a.types.canZeroField(path, candidate.Start)
	case argumentEmpty:
		zero := a.types.zeroOfParameter(path, candidate.Start, candidate.End)
		return zero != "" && zero == candidate.Replacement
	case branchIf, branchElse, branchCase:
		return !a.sources.causesMissingReturn(candidate.File, candidate.Start, candidate.End)
	}
	return true
}

func (a *adapter) Function(file string, offset int) string {
	return a.sources.function(file, offset)
}

func (a *adapter) Uncovered(ctx context.Context, mutants []mutant.Mutant, changed diff.Lines) (map[mutant.ID]string, error) {
	return a.coverage.uncovered(ctx, mutants, changed)
}

func (a *adapter) CallerGaps(ctx context.Context, changed diff.Lines) ([]language.CallerGap, error) {
	return a.callerGaps.find(ctx, changed)
}

func (a *adapter) Runner() mutant.Runner {
	return a.runner
}

var _ language.Adapter = (*adapter)(nil)

var _ language.CallerGapFinder = (*adapter)(nil)

var _ mutant.Runner = (*runner)(nil)
