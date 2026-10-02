package python

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/process"
	"golang.org/x/sync/errgroup"
)

const (
	pytestTestsFailed = 1
	pytestNoTests     = 5
	coverageFileName  = "coverage.json"
)

type coverage struct {
	settings Settings
	projects *projects
	mutex    sync.Mutex
	runs     map[string]*coverageRun
}

type coverageRun struct {
	once      sync.Once
	project   project
	noTests   bool
	files     map[string]coveredFile
	durations map[string]time.Duration
	startup   time.Duration
	err       error
}

// coveredFile holds the first line of each statement. coverage.py counts a statement on more than one line
// at its first line.
type coveredFile struct {
	statements []int
	missing    map[int]bool
	tests      map[int][]string
}

// imported gives false when no statement of the file ran, because an import runs its def and class lines.
func (f coveredFile) imported() bool {
	return len(f.missing) < len(f.statements)
}

func (f coveredFile) statementOf(line int) (int, bool) {
	index, found := slices.BinarySearch(f.statements, line)
	if found {
		return f.statements[index], true
	}
	if index == 0 {
		return 0, false
	}
	return f.statements[index-1], true
}

// testsOf gives nil when each test of the project must run: the coverage does not know the file, or the line
// runs only when a module loads.
func (r *coverageRun) testsOf(m mutant.Mutant) []string {
	file, found := r.files[m.File]
	if !found {
		return nil
	}
	statement, found := file.statementOf(m.Line)
	if !found {
		return nil
	}
	return file.tests[statement]
}

func (r *coverageRun) duration(tests []string) time.Duration {
	var total time.Duration
	for test, duration := range r.durations {
		if len(tests) == 0 || slices.Contains(tests, test) {
			total += duration
		}
	}
	return r.startup + total
}

func newCoverage(settings Settings, projects *projects) *coverage {
	return &coverage{settings: settings, projects: projects, runs: map[string]*coverageRun{}}
}

func (c *coverage) uncovered(ctx context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error) {
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(max(1, c.settings.Workers))
	measured := map[string]bool{}
	for _, m := range mutants {
		project := c.projects.of(m.File)
		if !measured[project.folder] {
			measured[project.folder] = true
			group.Go(func() error {
				_, err := c.run(groupContext, project)
				return err
			})
		}
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	uncovered := map[mutant.ID]string{}
	for _, m := range mutants {
		run, err := c.run(ctx, c.projects.of(m.File))
		if err != nil {
			return nil, err
		}
		if run.noTests {
			uncovered[m.ID] = fmt.Sprintf("the Python project in %s has no tests", run.project.path)
			continue
		}
		file, found := run.files[m.File]
		if !found || !file.imported() {
			uncovered[m.ID] = fmt.Sprintf("no test imports %s", m.File)
			continue
		}
		if statement, found := file.statementOf(m.Line); found && file.missing[statement] {
			uncovered[m.ID] = ""
		}
	}
	return uncovered, nil
}

func (c *coverage) run(ctx context.Context, project project) (*coverageRun, error) {
	c.mutex.Lock()
	run, found := c.runs[project.folder]
	if !found {
		run = &coverageRun{project: project}
		c.runs[project.folder] = run
	}
	c.mutex.Unlock()
	run.once.Do(func() { run.err = c.measure(ctx, run) })
	return run, run.err
}

func (c *coverage) measure(ctx context.Context, run *coverageRun) error {
	folder, err := os.MkdirTemp("", "mutants-coverage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(folder)
	if err := writeSupport(folder); err != nil {
		return err
	}
	output := filepath.Join(folder, coverageFileName)
	python := c.projects.python(run.project)
	test := process.Command{
		Program:   python[0],
		Arguments: append(python[1:], pytestArguments()...),
		Folder:    run.project.folder,
		// coverage.py records no test for each line with its sysmon core, which it takes on Python 3.12 and later
		Env: append(testEnv(folder), "MUTANTS_COVERAGE="+output, "MUTANTS_PROJECT="+run.project.folder, "COVERAGE_CORE=ctrace"),
	}
	started := time.Now()
	tested, err := test.Run(ctx)
	elapsed := time.Since(started)
	if err != nil {
		return fmt.Errorf("start the tests of the Python project in %s: %w", run.project.path, err)
	}

	var report struct {
		Coverage bool `json:"coverage"`
		Files    map[string]struct {
			Statements []int               `json:"statements"`
			Missing    []int               `json:"missing"`
			Tests      map[string][]string `json:"tests"`
		} `json:"files"`
		Durations map[string]float64 `json:"durations"`
	}
	content, readErr := os.ReadFile(output)
	if readErr == nil {
		if err := json.Unmarshal(content, &report); err != nil {
			return fmt.Errorf("read the coverage of the Python project in %s: %w", run.project.path, err)
		}
	}
	switch {
	case readErr == nil && !report.Coverage:
		return fmt.Errorf("the Python project in %s has no coverage.py: add coverage or pytest-cov to its dev dependencies", run.project.path)
	case tested.Code == pytestNoTests:
		run.noTests = true
		return nil
	case tested.Code == pytestTestsFailed:
		return fmt.Errorf("the tests of the Python project in %s fail with the real code, so no mutant can get a verdict:\n%s", run.project.path, strings.TrimSpace(tested.Tail))
	case tested.Code != 0:
		return fmt.Errorf("pytest exited with code %d in the Python project in %s:\n%s", tested.Code, run.project.path, strings.TrimSpace(tested.Tail))
	case errors.Is(readErr, os.ErrNotExist):
		return fmt.Errorf("the tests of the Python project in %s wrote no coverage:\n%s", run.project.path, strings.TrimSpace(tested.Tail))
	case readErr != nil:
		return readErr
	}

	run.files = map[string]coveredFile{}
	for file, measured := range report.Files {
		covered := coveredFile{statements: measured.Statements, missing: map[int]bool{}, tests: map[int][]string{}}
		slices.Sort(covered.statements)
		for _, line := range measured.Missing {
			covered.missing[line] = true
		}
		for line, tests := range measured.Tests {
			number, err := strconv.Atoi(line)
			if err != nil {
				return fmt.Errorf("read the coverage of the Python project in %s: %w", run.project.path, err)
			}
			covered.tests[number] = tests
		}
		run.files[path.Join(run.project.path, filepath.ToSlash(file))] = covered
	}
	run.durations = map[string]time.Duration{}
	var total time.Duration
	for test, seconds := range report.Durations {
		run.durations[test] = time.Duration(seconds * float64(time.Second))
		total += run.durations[test]
	}
	run.startup = max(0, elapsed-total)
	return nil
}

func pytestArguments() []string {
	return []string{"-m", "pytest", "-q", "--no-header", "-rfE", "-p", "mutants_plugin", "-p", "no:cacheprovider", "-p", "no:xdist"}
}

// testEnv puts the import hook and the pytest plugin in folder first on the path of Python, and stops Python
// from writing bytecode next to the files of the repository.
func testEnv(folder string) []string {
	pythonPath := folder
	if existing := os.Getenv("PYTHONPATH"); existing != "" {
		pythonPath += string(os.PathListSeparator) + existing
	}
	return append(os.Environ(), "PYTHONPATH="+pythonPath, "PYTHONDONTWRITEBYTECODE=1")
}

func writeSupport(folder string) error {
	for _, name := range []string{"sitecustomize.py", "mutants_plugin.py"} {
		content, err := support.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(folder, name), content, 0o600); err != nil {
			return err
		}
	}
	return nil
}
