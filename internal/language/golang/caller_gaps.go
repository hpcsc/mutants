package golang

import (
	"cmp"
	"context"
	"go/ast"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
)

type callerGaps struct {
	root     string
	settings Settings
	finder   *packageFinder
	coverage *coverage
	types    *typeChecker
	sources  *sourceFiles
}

type caller struct {
	pkg     goPackage
	callees []goPackage
	reached map[string]bool
	blocks  map[string][]block
}

func (g *callerGaps) find(ctx context.Context, changed diff.Lines) ([]language.CallerGap, error) {
	changedPackages := g.changedPackages(ctx, changed)
	var callers []*caller
	for _, importPath := range slices.Sorted(maps.Keys(changedPackages)) {
		c := &caller{pkg: changedPackages[importPath], reached: map[string]bool{}}
		loaded := g.types.load(c.pkg.Dir)
		if loaded == nil {
			continue
		}
		for _, imported := range loaded.Types.Imports() {
			callee, isChanged := changedPackages[imported.Path()]
			if !isChanged {
				continue
			}
			entries := g.entries(c.pkg, callee, changed)
			if len(entries) == 0 {
				continue
			}
			for name := range g.reach(callee, entries) {
				c.reached[name] = true
			}
			c.callees = append(c.callees, callee)
		}
		if len(c.callees) > 0 {
			callers = append(callers, c)
		}
	}
	if len(callers) == 0 {
		return nil, nil
	}

	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(max(1, g.settings.Workers))
	for _, c := range callers {
		group.Go(func() error {
			measured, err := g.coverage.profile(groupContext, c.pkg, c.callees)
			c.blocks = measured.blocks
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	var gaps []language.CallerGap
	for _, importPath := range slices.Sorted(maps.Keys(changedPackages)) {
		found, err := g.gaps(ctx, changedPackages[importPath], callers, changed)
		if err != nil {
			return nil, err
		}
		gaps = append(gaps, found...)
	}
	return gaps, nil
}

// changedPackages skips a folder that go list cannot read, because no mutant runs there either.
func (g *callerGaps) changedPackages(ctx context.Context, changed diff.Lines) map[string]goPackage {
	found := map[string]goPackage{}
	for _, file := range changed.Files() {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		pkg, err := g.finder.find(ctx, filepath.Dir(filepath.Join(g.root, file)))
		if err == nil && pkg.builds(filepath.Base(file)) {
			found[pkg.ImportPath] = pkg
		}
	}
	return found
}

func (g *callerGaps) entries(pkg, callee goPackage, changed diff.Lines) []string {
	loaded := g.types.load(pkg.Dir)
	if loaded == nil {
		return nil
	}
	var names []string
	for _, syntax := range loaded.Syntax {
		file := g.relative(loaded.Fset.File(syntax.Pos()).Name())
		ast.Inspect(syntax, func(node ast.Node) bool {
			if node == nil || !changed.Has(file, loaded.Fset.Position(node.Pos()).Line) {
				return true
			}
			switch node := node.(type) {
			case *ast.Ident:
				if function, ok := loaded.TypesInfo.Uses[node].(*types.Func); ok && function.Pkg() != nil && function.Pkg().Path() == callee.ImportPath {
					names = append(names, function.Origin().FullName())
				}
			case *ast.CompositeLit:
				names = append(names, g.methods(loaded.TypesInfo.TypeOf(node), callee.ImportPath)...)
			}
			return true
		})
	}
	return names
}

// a caller that gets a value from a constructor can call each method of that value
func (g *callerGaps) reach(pkg goPackage, entries []string) map[string]bool {
	loaded := g.types.load(pkg.Dir)
	if loaded == nil {
		return nil
	}
	declarations := map[string]*ast.FuncDecl{}
	functions := map[string]*types.Func{}
	for _, syntax := range loaded.Syntax {
		for _, declaration := range syntax.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				if object, ok := loaded.TypesInfo.Defs[function.Name].(*types.Func); ok {
					declarations[object.FullName()], functions[object.FullName()] = function, object
				}
			}
		}
	}
	reached := map[string]bool{}
	queue := slices.Clone(entries)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		declaration, found := declarations[name]
		if !found || reached[name] {
			continue
		}
		reached[name] = true
		results := functions[name].Signature().Results()
		for i := range results.Len() {
			queue = append(queue, g.methods(results.At(i).Type(), pkg.ImportPath)...)
		}
		if declaration.Body == nil {
			continue
		}
		ast.Inspect(declaration.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.Ident:
				if function, ok := loaded.TypesInfo.Uses[node].(*types.Func); ok && function.Pkg() != nil && function.Pkg().Path() == pkg.ImportPath {
					queue = append(queue, function.Origin().FullName())
				}
			case *ast.CompositeLit:
				queue = append(queue, g.methods(loaded.TypesInfo.TypeOf(node), pkg.ImportPath)...)
			}
			return true
		})
	}
	return reached
}

func (g *callerGaps) methods(value types.Type, importPath string) []string {
	if value == nil {
		return nil
	}
	if pointer, ok := types.Unalias(value).(*types.Pointer); ok {
		value = pointer.Elem()
	}
	named, ok := types.Unalias(value).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != importPath {
		return nil
	}
	methods := types.NewMethodSet(types.NewPointer(named.Origin()))
	names := make([]string, 0, methods.Len())
	for i := range methods.Len() {
		if function, ok := methods.At(i).Obj().(*types.Func); ok {
			names = append(names, function.Origin().FullName())
		}
	}
	return names
}

func (g *callerGaps) gaps(ctx context.Context, pkg goPackage, callers []*caller, changed diff.Lines) ([]language.CallerGap, error) {
	var reachers []*caller
	for _, c := range callers {
		if slices.ContainsFunc(c.callees, func(callee goPackage) bool { return callee.ImportPath == pkg.ImportPath }) {
			reachers = append(reachers, c)
		}
	}
	if len(reachers) == 0 {
		return nil, nil
	}
	own, err := g.coverage.run(ctx, pkg.Dir)
	if err != nil || own.noTests {
		return nil, err
	}
	loaded := g.types.load(pkg.Dir)
	if loaded == nil {
		return nil, nil
	}
	var gaps []language.CallerGap
	for _, syntax := range loaded.Syntax {
		lines := loaded.Fset.File(syntax.Pos())
		file := g.relative(lines.Name())
		for _, declaration := range syntax.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if gap, found := g.gapIn(loaded, file, function, reachers, own.blocks[file], changed); found {
				gaps = append(gaps, gap)
			}
		}
	}
	slices.SortFunc(gaps, func(a, b language.CallerGap) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Lines[0], b.Lines[0]))
	})
	return gaps, nil
}

func (g *callerGaps) gapIn(loaded *packages.Package, file string, function *ast.FuncDecl, reachers []*caller, own []block, changed diff.Lines) (language.CallerGap, bool) {
	object, ok := loaded.TypesInfo.Defs[function.Name].(*types.Func)
	if !ok {
		return language.CallerGap{}, false
	}
	var callers []*caller
	for _, c := range reachers {
		if c.reached[object.FullName()] {
			callers = append(callers, c)
		}
	}
	if len(callers) == 0 {
		return language.CallerGap{}, false
	}
	gap := language.CallerGap{File: file, Function: g.sources.funcName(function)}
	for _, line := range g.statementLines(loaded, function) {
		runs := func(b block) bool { return b.runs(line) }
		if !changed.Has(file, line) || !slices.ContainsFunc(own, runs) {
			continue
		}
		if !slices.ContainsFunc(callers, func(c *caller) bool { return slices.ContainsFunc(c.blocks[file], runs) }) {
			gap.Lines = append(gap.Lines, line)
		}
	}
	for _, c := range callers {
		gap.Callers = append(gap.Callers, g.relative(c.pkg.Dir))
	}
	return gap, len(gap.Lines) > 0
}

// a version of Go can start a coverage block on the line of the brace before the statement
func (g *callerGaps) statementLines(loaded *packages.Package, function *ast.FuncDecl) []int {
	if function.Body == nil {
		return nil
	}
	lines := map[int]bool{}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if statement, ok := node.(ast.Stmt); ok {
			if _, isBlock := statement.(*ast.BlockStmt); !isBlock {
				lines[loaded.Fset.Position(statement.Pos()).Line] = true
			}
		}
		return true
	})
	return slices.Sorted(maps.Keys(lines))
}

func (g *callerGaps) relative(path string) string {
	relative, err := filepath.Rel(g.root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relative)
}
