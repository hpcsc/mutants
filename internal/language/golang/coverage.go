package golang

import (
	"bufio"
	"context"
	"fmt"
	"go/ast"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/process"
	"golang.org/x/sync/errgroup"
)

type coverage struct {
	root       string
	settings   Settings
	finder     *packageFinder
	types      *typeChecker
	sources    *sourceFiles
	mutex      sync.Mutex
	runs       map[string]*coverageRun
	callerRuns map[string]*coverageRun
	testers    map[mutant.ID][]*coverageRun
}

type coverageRun struct {
	once     sync.Once
	pkg      goPackage
	noTests  bool
	blocks   map[string][]block
	baseline baseline
	err      error
}

type baseline struct {
	build, test time.Duration
}

// block ends before endColumn, as in the Go coverage profile.
type block struct {
	startLine, startColumn int
	endLine, endColumn     int
	count                  int
}

func (b block) holds(line, column int) bool {
	afterStart := line > b.startLine || line == b.startLine && column >= b.startColumn
	beforeEnd := line < b.endLine || line == b.endLine && column < b.endColumn
	return afterStart && beforeEnd
}

func (b block) runs(line int) bool {
	return b.count > 0 && b.startLine <= line && line <= b.endLine
}

func newCoverage(root string, settings Settings, finder *packageFinder, types *typeChecker, sources *sourceFiles) *coverage {
	return &coverage{
		root: root, settings: settings, finder: finder, types: types, sources: sources,
		runs: map[string]*coverageRun{}, callerRuns: map[string]*coverageRun{}, testers: map[mutant.ID][]*coverageRun{},
	}
}

// uncovered keeps a mutant that the tests of its own package do not run when the tests of a changed caller run
// it, and the runner then tests the mutant with the tests of those callers.
func (c *coverage) uncovered(ctx context.Context, mutants []mutant.Mutant, changed diff.Lines) (map[mutant.ID]string, error) {
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(max(1, c.settings.Workers))
	measured := map[string]bool{}
	for _, m := range mutants {
		folder := filepath.Dir(filepath.Join(c.root, m.File))
		if !measured[folder] {
			measured[folder] = true
			group.Go(func() error {
				_, err := c.run(groupContext, folder)
				return err
			})
		}
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	uncovered := map[mutant.ID]string{}
	callees := map[string]bool{}
	for _, m := range mutants {
		run, err := c.run(ctx, filepath.Dir(filepath.Join(c.root, m.File)))
		if err != nil {
			return nil, err
		}
		switch {
		case run.noTests:
			uncovered[m.ID] = fmt.Sprintf("package %s has no test files", filepath.ToSlash(filepath.Dir(m.File)))
		case !c.covers(run.blocks[m.File], m):
			uncovered[m.ID] = ""
		default:
			continue
		}
		callees[run.pkg.ImportPath] = true
	}
	if len(uncovered) == 0 {
		return uncovered, nil
	}

	callers, err := c.callers(ctx, changed, callees)
	if err != nil {
		return nil, err
	}
	for _, m := range mutants {
		if _, found := uncovered[m.ID]; !found {
			continue
		}
		own, err := c.run(ctx, filepath.Dir(filepath.Join(c.root, m.File)))
		if err != nil {
			return nil, err
		}
		var testers []*coverageRun
		for _, caller := range callers[own.pkg.ImportPath] {
			if blocks := caller.blocks[m.File]; len(blocks) > 0 && c.covers(blocks, m) {
				testers = append(testers, caller)
			}
		}
		if len(testers) > 0 {
			delete(uncovered, m.ID)
			c.mutex.Lock()
			c.testers[m.ID] = testers
			c.mutex.Unlock()
		}
	}
	return uncovered, nil
}

// Go's profile has no block for the code after a function literal, and from Go 1.27 none for the brace that opens
// a branch, so a mutant that no block holds runs when a test enters its function.
func (c *coverage) covers(blocks []block, m mutant.Mutant) bool {
	heldByZero, lineRuns := false, false
	for _, b := range blocks {
		if b.runs(m.Line) {
			lineRuns = true
		}
		if b.count == 0 && b.holds(m.Line, m.Column) {
			heldByZero = true
		}
	}
	switch {
	case lineRuns:
		return true
	case heldByZero:
		return false
	}
	return c.entered(blocks, m)
}

func (c *coverage) entered(blocks []block, m mutant.Mutant) bool {
	syntax, lines, err := c.sources.parse(m.File)
	if err != nil || m.Start > lines.Size() {
		return true
	}
	position := lines.Pos(m.Start)
	for _, declaration := range syntax.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || position < function.Pos() || position >= function.End() {
			continue
		}
		first, last := lines.Line(function.Pos()), lines.Line(function.End())
		return slices.ContainsFunc(blocks, func(b block) bool { return b.count > 0 && first <= b.startLine && b.endLine <= last })
	}
	return true
}

// testersOf gives nil when the tests of the package of the mutant run it.
func (c *coverage) testersOf(id mutant.ID) []*coverageRun {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.testers[id]
}

// callers runs the tests of each changed package that imports a package of callees one time, and gives them by
// the import path of each callee.
func (c *coverage) callers(ctx context.Context, changed diff.Lines, callees map[string]bool) (map[string][]*coverageRun, error) {
	changedPackages := c.changedPackages(ctx, changed)
	byCallee := map[string][]goPackage{}
	var callers []goPackage
	for _, importPath := range slices.Sorted(maps.Keys(changedPackages)) {
		caller := changedPackages[importPath]
		calls := false
		for _, imported := range c.changedImports(caller, changedPackages) {
			if callees[imported.ImportPath] {
				byCallee[imported.ImportPath] = append(byCallee[imported.ImportPath], caller)
				calls = true
			}
		}
		if calls {
			callers = append(callers, caller)
		}
	}

	measured := map[string]*coverageRun{}
	var mutex sync.Mutex
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(max(1, c.settings.Workers))
	for _, caller := range callers {
		group.Go(func() error {
			run, err := c.callerRun(groupContext, caller, c.changedImports(caller, changedPackages))
			mutex.Lock()
			measured[caller.ImportPath] = run
			mutex.Unlock()
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	runs := map[string][]*coverageRun{}
	for callee, of := range byCallee {
		for _, caller := range of {
			runs[callee] = append(runs[callee], measured[caller.ImportPath])
		}
	}
	return runs, nil
}

// changedPackages skips a folder that go list cannot read, because no mutant runs there either.
func (c *coverage) changedPackages(ctx context.Context, changed diff.Lines) map[string]goPackage {
	found := map[string]goPackage{}
	for _, file := range changed.Files() {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		pkg, err := c.finder.find(ctx, filepath.Dir(filepath.Join(c.root, file)))
		if err == nil && pkg.builds(filepath.Base(file)) {
			found[pkg.ImportPath] = pkg
		}
	}
	return found
}

func (c *coverage) changedImports(pkg goPackage, changedPackages map[string]goPackage) []goPackage {
	loaded := c.types.load(pkg.Dir)
	if loaded == nil {
		return nil
	}
	var imported []goPackage
	for _, i := range loaded.Types.Imports() {
		if callee, isChanged := changedPackages[i.Path()]; isChanged {
			imported = append(imported, callee)
		}
	}
	slices.SortFunc(imported, func(a, b goPackage) int { return strings.Compare(a.ImportPath, b.ImportPath) })
	return imported
}

// callerRun measures what the tests of caller run in the packages of callees, one time for each caller and callees.
func (c *coverage) callerRun(ctx context.Context, caller goPackage, callees []goPackage) (*coverageRun, error) {
	key := caller.Dir
	for _, callee := range callees {
		key += "\x00" + callee.ImportPath
	}
	c.mutex.Lock()
	run, found := c.callerRuns[key]
	if !found {
		run = &coverageRun{pkg: caller}
		c.callerRuns[key] = run
	}
	c.mutex.Unlock()
	run.once.Do(func() {
		var measured profiled
		measured, run.err = c.profile(ctx, caller, callees)
		run.noTests, run.blocks, run.baseline = measured.noTests, measured.blocks, measured.baseline
	})
	return run, run.err
}

func (c *coverage) run(ctx context.Context, folder string) (*coverageRun, error) {
	pkg, err := c.finder.find(ctx, folder)
	if err != nil {
		return nil, err
	}
	c.mutex.Lock()
	run, found := c.runs[pkg.Dir]
	if !found {
		run = &coverageRun{pkg: pkg}
		c.runs[pkg.Dir] = run
	}
	c.mutex.Unlock()
	run.once.Do(func() { run.err = c.measure(ctx, run) })
	return run, run.err
}

func (c *coverage) measure(ctx context.Context, run *coverageRun) error {
	measured, err := c.profile(ctx, run.pkg, []goPackage{run.pkg})
	run.noTests, run.blocks, run.baseline = measured.noTests, measured.blocks, measured.baseline
	return err
}

type profiled struct {
	noTests  bool
	blocks   map[string][]block
	baseline baseline
}

func (c *coverage) profile(ctx context.Context, pkg goPackage, covered []goPackage) (profiled, error) {
	var result profiled
	folder, err := os.MkdirTemp("", "mutants-coverage-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(folder)
	binary := filepath.Join(folder, "cover.test")
	arguments := []string{"test", "-c", "-cover", "-covermode=set"}
	folderOf := map[string]string{}
	var paths []string
	for _, p := range covered {
		folderOf[p.ImportPath] = p.Dir
		paths = append(paths, p.ImportPath)
	}
	if len(covered) != 1 || covered[0].ImportPath != pkg.ImportPath {
		arguments = append(arguments, "-coverpkg="+strings.Join(paths, ","))
	}
	build := process.Command{
		Program:   "go",
		Arguments: append(append(append(arguments, "-o", binary), c.settings.tagArguments()...), "."),
		Folder:    pkg.Dir,
		Env:       c.settings.buildEnv(),
	}
	started := time.Now()
	built, err := build.Run(ctx)
	result.baseline.build = time.Since(started)
	if err != nil {
		return result, err
	}
	if built.Code != 0 {
		return result, fmt.Errorf("build the tests of %s: %s", pkg.ImportPath, strings.TrimSpace(built.Tail))
	}
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		result.noTests = true
		return result, nil
	}

	profile := filepath.Join(folder, "cover.out")
	test := process.Command{
		Program:   binary,
		Arguments: []string{"-test.count=1", "-test.timeout=10m", "-test.coverprofile=" + profile},
		Folder:    pkg.Dir,
		Env:       c.settings.testEnv(),
	}
	started = time.Now()
	tested, err := test.Run(ctx)
	result.baseline.test = time.Since(started)
	if err != nil {
		return result, err
	}
	if tested.Code != 0 {
		return result, fmt.Errorf("the tests of %s fail with the real code, so no mutant can get a verdict:\n%s", pkg.ImportPath, strings.TrimSpace(tested.Tail))
	}
	result.blocks, err = c.readProfile(folderOf, profile)
	return result, err
}

func (c *coverage) readProfile(folderOf map[string]string, profile string) (map[string][]block, error) {
	file, err := os.Open(profile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	blocks := map[string][]block{}
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		line := lines.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		name, rest, found := strings.Cut(line, ":")
		var b block
		var statements int
		if !found {
			return nil, fmt.Errorf("read the coverage line %q", line)
		}
		if _, err := fmt.Sscanf(rest, "%d.%d,%d.%d %d %d", &b.startLine, &b.startColumn, &b.endLine, &b.endColumn, &statements, &b.count); err != nil {
			return nil, fmt.Errorf("read the coverage line %q: %w", line, err)
		}
		dir, found := folderOf[path.Dir(name)]
		if !found {
			continue
		}
		relative, err := filepath.Rel(c.root, filepath.Join(dir, path.Base(name)))
		if err != nil {
			return nil, err
		}
		key := filepath.ToSlash(relative)
		blocks[key] = append(blocks[key], b)
	}
	return blocks, lines.Err()
}
