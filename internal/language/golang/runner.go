package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/process"
	"golang.org/x/tools/go/ast/astutil"
)

const (
	baselineFactor = 3
	limitMargin    = 5 * time.Second
)

var (
	unusedImport   = regexp.MustCompile(`"([^"]+)" imported (?:as \S+ )?and not used`)
	unusedVariable = regexp.MustCompile(`(\S+\.go):(\d+):(\d+): (?:declared and not used: (\S+)|(\S+) declared and not used)`)
)

// runner runs the test binary itself, because go test can give a verdict from its cache.
type runner struct {
	root      string
	settings  Settings
	finder    *packageFinder
	coverage  *coverage
	userCache func() string
}

func (r *runner) Run(ctx context.Context, m mutant.Mutant) (mutant.Verdict, error) {
	path := filepath.Join(r.root, m.File)
	pkg, err := r.finder.find(ctx, filepath.Dir(path))
	if err != nil {
		return mutant.Verdict{Status: mutant.InfraError, Detail: err.Error()}, nil
	}
	baseline, err := r.coverage.baseline(ctx, pkg.Dir)
	if err != nil {
		return mutant.Verdict{}, err
	}

	folder, err := os.MkdirTemp("", "mutants-mutant-")
	if err != nil {
		return mutant.Verdict{}, err
	}
	defer os.RemoveAll(folder)
	original := filepath.Join(pkg.Dir, filepath.Base(path))
	content, err := r.mutate(original, m)
	if err != nil {
		return mutant.Verdict{Status: mutant.InfraError, Detail: err.Error()}, nil
	}
	binary := filepath.Join(folder, "pkg.test")
	buildLimit := max(r.settings.BuildLimit, (baselineFactor * baseline.build).Round(time.Second))
	built, err := r.build(ctx, pkg, folder, original, content, binary, buildLimit)
	if err == nil && built.Code != 0 {
		if used := r.blankImports(r.useVariables(content, filepath.Base(original), built.Tail), built.Tail); used != content {
			built, err = r.build(ctx, pkg, folder, original, used, binary, buildLimit)
		}
	}
	switch {
	case err != nil:
		return mutant.Verdict{}, err
	case built.TimedOut:
		return mutant.Verdict{Status: mutant.InfraError, Detail: fmt.Sprintf("the build ran past %s", buildLimit)}, nil
	case built.Code != 0 && strings.Contains(built.Tail, "GOCACHEPROG"):
		return mutant.Verdict{Status: mutant.InfraError, Detail: "the build cache failed: " + strings.TrimSpace(built.Tail)}, nil
	case built.Code != 0:
		return mutant.Verdict{Status: mutant.NotViable, Detail: strings.TrimSpace(built.Tail)}, nil
	}
	if _, err := os.Stat(binary); err != nil {
		return mutant.Verdict{Status: mutant.InfraError, Detail: "go test -c made no test binary: " + err.Error()}, nil
	}

	limit := baselineFactor*baseline.test + limitMargin
	tested, err := r.test(ctx, pkg, binary, limit)
	// the load of the host can grow after the baseline, so a second run with twice the limit decides
	if err == nil && tested.TimedOut {
		limit *= 2
		tested, err = r.test(ctx, pkg, binary, limit)
	}
	if err != nil {
		return mutant.Verdict{}, err
	}
	return r.verdict(tested, limit), nil
}

func (r *runner) test(ctx context.Context, pkg goPackage, binary string, limit time.Duration) (process.Exit, error) {
	test := process.Command{
		Program:   binary,
		Arguments: []string{"-test.count=1", "-test.failfast"},
		Folder:    pkg.Dir,
		Env:       r.settings.testEnv(),
		Limit:     limit,
	}
	return test.Run(ctx)
}

func (r *runner) mutate(path string, m mutant.Mutant) (string, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if m.Start < 0 || m.End > len(source) || string(source[m.Start:m.End]) != m.Original {
		return "", fmt.Errorf("%s changed after mutants read it", m.File)
	}
	return string(source[:m.Start]) + m.Replacement + string(source[m.End:]), nil
}

func (r *runner) build(ctx context.Context, pkg goPackage, folder, original, content, binary string, limit time.Duration) (process.Exit, error) {
	mutated := filepath.Join(folder, filepath.Base(original))
	if err := os.WriteFile(mutated, []byte(content), 0o600); err != nil {
		return process.Exit{}, err
	}
	replace, err := json.Marshal(map[string]map[string]string{"Replace": {original: mutated}})
	if err != nil {
		return process.Exit{}, err
	}
	overlay := filepath.Join(folder, "overlay.json")
	if err := os.WriteFile(overlay, replace, 0o600); err != nil {
		return process.Exit{}, err
	}
	build := process.Command{
		Program:   "go",
		Arguments: append(append([]string{"test", "-c", "-vet=off", "-overlay", overlay, "-o", binary}, r.settings.tagArguments()...), "."),
		Folder:    pkg.Dir,
		Env:       r.buildEnv(folder),
		Limit:     limit,
	}
	return build.Run(ctx)
}

// buildEnv sends the new entries of a mutant build to the cache of the mutant, because no later build
// reads them.
func (r *runner) buildEnv(folder string) []string {
	env := r.settings.buildEnv()
	userCache := r.userCache()
	if userCache == "" {
		return env
	}
	program, ok := quote(append(slices.Clone(r.settings.CacheProgram), userCache, filepath.Join(folder, "cache")))
	if !ok {
		return env
	}
	return append(env, "GOCACHEPROG="+program)
}

// userCache gives "" when the user has a cache program of their own, because mutants must not replace it.
func userCache(root string, cacheProgram []string) string {
	if len(cacheProgram) == 0 {
		return ""
	}
	command := exec.Command("go", "env", "-json", "GOCACHE", "GOCACHEPROG")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return ""
	}
	var env struct{ GOCACHE, GOCACHEPROG string }
	if json.Unmarshal(output, &env) != nil || env.GOCACHEPROG != "" {
		return ""
	}
	return env.GOCACHE
}

// quote follows go, which splits GOCACHEPROG at spaces, keeps a quoted field whole, and has no escape in it.
func quote(arguments []string) (string, bool) {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		switch {
		case !strings.Contains(argument, "'"):
			quoted = append(quoted, "'"+argument+"'")
		case !strings.Contains(argument, `"`):
			quoted = append(quoted, `"`+argument+`"`)
		default:
			return "", false
		}
	}
	return strings.Join(quoted, " "), true
}

func (r *runner) blankImports(content, compilerOutput string) string {
	unused := map[string]bool{}
	for _, match := range unusedImport.FindAllStringSubmatch(compilerOutput, -1) {
		unused[match[1]] = true
	}
	positions := token.NewFileSet()
	syntax, err := parser.ParseFile(positions, "", content, parser.ImportsOnly)
	if err != nil {
		return content
	}
	imports := slices.Clone(syntax.Imports)
	slices.Reverse(imports)
	for _, spec := range imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !unused[path] {
			continue
		}
		if spec.Name != nil {
			start, end := positions.Position(spec.Name.Pos()).Offset, positions.Position(spec.Name.End()).Offset
			content = content[:start] + "_" + content[end:]
			continue
		}
		start := positions.Position(spec.Path.Pos()).Offset
		content = content[:start] + "_ " + content[start:]
	}
	return content
}

func (r *runner) useVariables(content, file, compilerOutput string) string {
	matches := unusedVariable.FindAllStringSubmatch(compilerOutput, -1)
	if len(matches) == 0 {
		return content
	}
	positions := token.NewFileSet()
	syntax, err := parser.ParseFile(positions, "", content, parser.SkipObjectResolution)
	if err != nil {
		return content
	}
	lines := positions.File(syntax.Pos())
	type insert struct {
		offset int
		text   string
	}
	var inserts []insert
	for _, match := range matches {
		line, _ := strconv.Atoi(match[2])
		column, _ := strconv.Atoi(match[3])
		name := match[4] + match[5]
		if filepath.Base(match[1]) != file || line < 1 || line > lines.LineCount() {
			continue
		}
		for _, offset := range r.useOffsets(syntax, lines, lines.LineStart(line)+token.Pos(column-1), name) {
			inserts = append(inserts, insert{offset: offset, text: "; _ = " + name + ";"})
		}
	}
	slices.SortFunc(inserts, func(a, b insert) int { return b.offset - a.offset })
	for _, use := range inserts {
		content = content[:use.offset] + use.text + content[use.offset:]
	}
	return content
}

func (r *runner) useOffsets(syntax *ast.File, lines *token.File, position token.Pos, name string) []int {
	path, _ := astutil.PathEnclosingInterval(syntax, position, position+token.Pos(len(name)))
	if len(path) == 0 {
		return nil
	}
	if identifier, ok := path[0].(*ast.Ident); !ok || identifier.Name != name {
		return nil
	}
	for i := 1; i < len(path); i++ {
		child := path[i-1]
		switch parent := path[i].(type) {
		case *ast.BlockStmt, *ast.CaseClause:
			return []int{lines.Offset(child.End())}
		case *ast.CommClause:
			if child == parent.Comm {
				return []int{lines.Offset(parent.Colon) + 1}
			}
			return []int{lines.Offset(child.End())}
		case *ast.IfStmt:
			return []int{lines.Offset(parent.Body.Lbrace) + 1}
		case *ast.ForStmt:
			return []int{lines.Offset(parent.Body.Lbrace) + 1}
		case *ast.RangeStmt:
			return []int{lines.Offset(parent.Body.Lbrace) + 1}
		case *ast.SwitchStmt:
			return r.clauseStarts(lines, parent.Body)
		case *ast.TypeSwitchStmt:
			return r.clauseStarts(lines, parent.Body)
		case *ast.FuncDecl, *ast.FuncLit:
			return nil
		}
	}
	return nil
}

func (r *runner) clauseStarts(lines *token.File, body *ast.BlockStmt) []int {
	var offsets []int
	for _, clause := range body.List {
		if clause, ok := clause.(*ast.CaseClause); ok {
			offsets = append(offsets, lines.Offset(clause.Colon)+1)
		}
	}
	return offsets
}

func (r *runner) verdict(tested process.Exit, limit time.Duration) mutant.Verdict {
	switch {
	case tested.TimedOut:
		return mutant.Verdict{Status: mutant.TimedOut, Detail: fmt.Sprintf("the tests ran past %s", limit)}
	case tested.Code == 0 && !tested.Signaled:
		return mutant.Verdict{Status: mutant.Lived}
	case tested.Signaled:
		return mutant.Verdict{Status: mutant.InfraError, Detail: "a signal stopped the tests:\n" + strings.TrimSpace(tested.Tail)}
	case strings.Contains(tested.Tail, "runtime: out of memory"):
		return mutant.Verdict{Status: mutant.InfraError, Detail: "the tests ran out of memory"}
	}
	return mutant.Verdict{Status: mutant.Killed, Detail: r.failure(tested)}
}

func (r *runner) failure(tested process.Exit) string {
	var reasons []string
	for _, prefix := range []string{"--- FAIL:", "panic:", "fatal error:"} {
		for line := range strings.Lines(tested.Tail) {
			if strings.HasPrefix(line, prefix) {
				reasons = append(reasons, strings.TrimSpace(line))
				break
			}
		}
	}
	if len(reasons) == 0 {
		return fmt.Sprintf("the tests exited with code %d", tested.Code)
	}
	return strings.Join(reasons, "; ")
}
