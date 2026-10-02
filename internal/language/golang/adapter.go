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
	swapFields       = "SWAP_FIELDS"
	returnZero       = "RETURN_ZERO"
	returnErrorNil   = "RETURN_ERROR_NIL"
	returnTrue       = "RETURN_TRUE"
	integerDecrement = "INTEGER_DECREMENT"
	timeBoundary     = "TIME_BOUNDARY"
	calendarDay      = "CALENDAR_DAY"
	fieldZero        = "FIELD_ZERO"
	argumentZero     = "ARGUMENT_ZERO"
)

type Settings struct {
	Tags          []string
	BuildLimit    time.Duration
	Workers       int
	ZeroFunctions []string
	// CacheProgram is the GOCACHEPROG of a mutant build, before the folders of the user cache and the mutant
	// cache. When it is empty, mutant builds write to the cache of the user.
	CacheProgram []string
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
	root       string
	finder     *packageFinder
	sources    *sourceFiles
	types      *typeChecker
	coverage   *coverage
	callerGaps *callerGaps
	runner     *runner
}

func New(root string, settings Settings) language.Adapter {
	finder := newPackageFinder(settings.tagArguments())
	coverage := newCoverage(root, settings, finder)
	sources := newSourceFiles(root)
	types := newTypeChecker(settings.tagArguments(), settings.ZeroFunctions)
	return &adapter{
		root:       root,
		finder:     finder,
		sources:    sources,
		types:      types,
		coverage:   coverage,
		callerGaps: &callerGaps{root: root, settings: settings, finder: finder, coverage: coverage, types: types, sources: sources},
		runner:     &runner{root: root, settings: settings, finder: finder, coverage: coverage, userCache: sync.OnceValue(func() string { return userCache(root, settings.CacheProgram) })},
	}
}

func (a *adapter) Name() string {
	return "go"
}

func (a *adapter) Extensions() []string {
	return []string{".go"}
}

func (a *adapter) Keep(candidate operator.Edit) bool {
	if candidate.Replacement == candidate.Original || strings.HasSuffix(candidate.File, "_test.go") {
		return false
	}
	syntax, _, err := a.sources.parse(candidate.File)
	if err != nil || ast.IsGenerated(syntax) {
		return false
	}
	path := filepath.Join(a.root, candidate.File)
	pkg, err := a.finder.find(context.Background(), filepath.Dir(path))
	if err != nil || !pkg.builds(filepath.Base(path)) {
		return false
	}
	switch candidate.Operator {
	case swapFields:
		return a.types.canSwap(path, candidate.Start, candidate.End)
	case returnZero:
		zero := a.types.zeroOfSlot(path, candidate.Start, candidate.End)
		return zero != "" && zero == candidate.Replacement
	case returnErrorNil:
		return a.types.isErrorSlot(path, candidate.Start, candidate.End)
	case returnTrue:
		return a.types.canBecomeTrue(path, candidate.Start, candidate.End)
	case integerDecrement:
		return !a.sources.isZeroIndexOrSize(candidate.File, candidate.Start, candidate.End)
	case timeBoundary:
		return a.types.callsTimeMethod(path, candidate.Start, candidate.End)
	case calendarDay:
		return a.types.callsTimeMethod(path, candidate.Start, candidate.End) && a.sources.importsTime(candidate.File)
	case fieldZero:
		return a.types.canZeroField(path, candidate.Start)
	case argumentZero:
		zero := a.types.zeroOfParameter(path, candidate.Start, candidate.End)
		return zero != "" && zero == candidate.Replacement
	}
	return true
}

func (a *adapter) Function(file string, offset int) string {
	return a.sources.function(file, offset)
}

func (a *adapter) Uncovered(ctx context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error) {
	return a.coverage.uncovered(ctx, mutants)
}

func (a *adapter) CallerGaps(ctx context.Context, changed diff.Lines) ([]language.CallerGap, error) {
	return a.callerGaps.find(ctx, changed)
}

func (a *adapter) Runner() mutant.Runner {
	return a.runner
}

var _ language.Adapter = (*adapter)(nil)

var _ mutant.Runner = (*runner)(nil)
