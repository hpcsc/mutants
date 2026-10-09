package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	plainLimit = 15 * time.Minute
	maxCallers = 20
)

type plainResult struct {
	notBuilt string
	failure  string
	timedOut bool
}

func (p plainResult) passed() bool {
	return p.notBuilt == "" && p.failure == "" && !p.timedOut
}

type tester interface {
	folderOf(file string) string
	baseline(ctx context.Context, folder string) error
	testIn(ctx context.Context, folder string) (plainResult, error)
	test(ctx context.Context, file string) (plainResult, error)
	testCallers(ctx context.Context, file string) (plainResult, error)
}

func newTester(c *clone) tester {
	if c.Language == "go" {
		return &goTester{root: c.folder, baselines: map[string]error{}}
	}
	return &pythonTester{root: c.folder, python: c.python(), baselines: map[string]error{}}
}

type goTester struct {
	root      string
	baselines map[string]error
}

func (t *goTester) folderOf(file string) string {
	return filepath.Dir(filepath.Join(t.root, file))
}

func (t *goTester) baseline(ctx context.Context, folder string) error {
	if err, seen := t.baselines[folder]; seen {
		return err
	}
	result, err := t.testIn(ctx, folder)
	if err != nil {
		return err
	}
	t.baselines[folder] = nil
	if !result.passed() {
		t.baselines[folder] = fmt.Errorf("the plain tests of %s fail with the real code: %s", relative(t.root, folder), firstLine(result.notBuilt+result.failure))
	}
	return t.baselines[folder]
}

func (t *goTester) test(ctx context.Context, file string) (plainResult, error) {
	return t.testIn(ctx, t.folderOf(file))
}

func (t *goTester) testIn(ctx context.Context, folder string) (plainResult, error) {
	built, err := goCommand(folder, "test", "-c", "-vet=off", "-o", os.DevNull, ".").run(ctx)
	switch {
	case err != nil:
		return plainResult{}, err
	case built.code != 0:
		return plainResult{notBuilt: strings.TrimSpace(built.output())}, nil
	}
	tested, err := goCommand(folder, "test", "-count=1", "-vet=off", "-failfast", ".").run(ctx)
	switch {
	case err != nil:
		return plainResult{}, err
	case tested.timedOut:
		return plainResult{timedOut: true}, nil
	case tested.code != 0:
		return plainResult{failure: goFailure(tested.output())}, nil
	}
	return plainResult{}, nil
}

// mutants also tests a mutant with the tests of a changed caller, so a KILLED mutant can pass the tests of its own package
func (t *goTester) testCallers(ctx context.Context, file string) (plainResult, error) {
	callers, err := t.callers(ctx, t.folderOf(file))
	if err != nil {
		return plainResult{}, err
	}
	for _, caller := range callers {
		if t.baseline(ctx, caller) != nil {
			continue
		}
		result, err := t.testIn(ctx, caller)
		if err != nil {
			return plainResult{}, err
		}
		if result.failure != "" {
			return plainResult{failure: relative(t.root, caller) + ": " + result.failure}, nil
		}
	}
	return plainResult{}, nil
}

func (t *goTester) callers(ctx context.Context, folder string) ([]string, error) {
	listed, err := goCommand(folder, "list", "-f", "{{.ImportPath}}\t{{.Module.Dir}}", ".").run(ctx)
	if err != nil || listed.code != 0 {
		return nil, errors.Join(err, errors.New(strings.TrimSpace(listed.stderr)))
	}
	importPath, module, _ := strings.Cut(strings.TrimSpace(listed.stdout), "\t")
	format := `{{.Dir}}{{"\t"}}{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}`
	packages, err := goCommand(module, "list", "-e", "-f", format, "./...").run(ctx)
	if err != nil || packages.code != 0 {
		return nil, errors.Join(err, errors.New(strings.TrimSpace(packages.stderr)))
	}
	var callers []string
	for line := range strings.Lines(packages.stdout) {
		dir, imports, _ := strings.Cut(strings.TrimSpace(line), "\t")
		if dir != folder && slices.Contains(strings.Fields(imports), importPath) && len(callers) < maxCallers {
			callers = append(callers, dir)
		}
	}
	return callers, nil
}

func relative(root, folder string) string {
	path, err := filepath.Rel(root, folder)
	if err != nil {
		return folder
	}
	return path
}

func goCommand(folder string, arguments ...string) command {
	return command{program: "go", arguments: arguments, folder: folder, limit: plainLimit}
}

func goFailure(output string) string {
	for _, prefix := range []string{"--- FAIL:", "panic:", "fatal error:", "FAIL"} {
		for line := range strings.Lines(output) {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				return strings.TrimSpace(line)
			}
		}
	}
	return firstLine(output)
}

type pythonTester struct {
	root      string
	python    string
	baselines map[string]error
}

var pythonProjectFiles = []string{"pyproject.toml", "setup.cfg", "setup.py", "pytest.ini", "tox.ini"}

func (t *pythonTester) folderOf(file string) string {
	for folder := filepath.Dir(filepath.Join(t.root, file)); strings.HasPrefix(folder, t.root) && folder != t.root; folder = filepath.Dir(folder) {
		for _, name := range pythonProjectFiles {
			if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
				return folder
			}
		}
	}
	return t.root
}

func (t *pythonTester) baseline(ctx context.Context, folder string) error {
	if err, seen := t.baselines[folder]; seen {
		return err
	}
	result, err := t.testIn(ctx, folder)
	if err != nil {
		return err
	}
	t.baselines[folder] = nil
	if !result.passed() {
		t.baselines[folder] = fmt.Errorf("the plain tests of the project in %s fail with the real code: %s", relative(t.root, folder), result.failure)
	}
	return t.baselines[folder]
}

func (t *pythonTester) test(ctx context.Context, file string) (plainResult, error) {
	path := filepath.Join(t.root, file)
	compiled, err := t.command(t.folderOf(file), "-c", `import sys; compile(open(sys.argv[1], "rb").read(), sys.argv[1], "exec")`, path).run(ctx)
	switch {
	case err != nil:
		return plainResult{}, err
	case compiled.code != 0:
		return plainResult{notBuilt: strings.TrimSpace(compiled.output())}, nil
	}
	return t.testIn(ctx, t.folderOf(file))
}

// testIn runs all the tests of the project, so a test that the selection of mutants leaves out also counts
func (t *pythonTester) testIn(ctx context.Context, folder string) (plainResult, error) {
	tested, err := t.command(folder, "-m", "pytest", "-q", "-x", "-rfE", "-p", "no:cacheprovider").run(ctx)
	switch {
	case err != nil:
		return plainResult{}, err
	case tested.timedOut:
		return plainResult{timedOut: true}, nil
	case tested.code != 0:
		return plainResult{failure: pythonFailure(tested.code, tested.output())}, nil
	}
	return plainResult{}, nil
}

func (t *pythonTester) testCallers(context.Context, string) (plainResult, error) {
	return plainResult{}, nil
}

func (t *pythonTester) command(folder string, arguments ...string) command {
	return command{
		program:   t.python,
		arguments: arguments,
		folder:    folder,
		env:       append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1"),
		limit:     plainLimit,
	}
}

func pythonFailure(code int, output string) string {
	for line := range strings.Lines(output) {
		if strings.HasPrefix(line, "FAILED ") || strings.HasPrefix(line, "ERROR ") {
			return strings.TrimSpace(line)
		}
	}
	return fmt.Sprintf("pytest exited with code %d: %s", code, firstLine(output))
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}
