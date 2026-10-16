package elixir

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/process"
	"golang.org/x/sync/errgroup"
)

const (
	mixTestsFailed   = 2
	coverageFileName = "coverage.json"
	taskFileName     = "mutants_task.exs"
)

type coverage struct {
	settings Settings
	projects *projects
	mutex    sync.Mutex
	runs     map[string]*coverageRun
}

type coverageRun struct {
	once     sync.Once
	project  project
	noTests  bool
	files    map[string]coveredFile
	baseline time.Duration
	err      error
}

type coveredFile struct {
	lines   []int
	missing map[int]bool
}

type lineRange struct {
	first, last int
}

type sourceLines interface {
	clauseHeadOf(file string, offset int) (clauseHead, bool)
	functionLines(file string, offset int) []lineRange
}

func (f coveredFile) called() bool {
	return len(f.missing) < len(f.lines)
}

func (f coveredFile) counted(ranges []lineRange) bool {
	return slices.ContainsFunc(f.lines, func(line int) bool { return within(line, ranges) })
}

func (f coveredFile) ran(ranges []lineRange) bool {
	return slices.ContainsFunc(f.lines, func(line int) bool { return within(line, ranges) && !f.missing[line] })
}

func within(line int, ranges []lineRange) bool {
	return slices.ContainsFunc(ranges, func(r lineRange) bool { return r.first <= line && line <= r.last })
}

func newCoverage(settings Settings, projects *projects) *coverage {
	return &coverage{settings: settings, projects: projects, runs: map[string]*coverageRun{}}
}

func (c *coverage) uncovered(ctx context.Context, mutants []mutant.Mutant, source sourceLines) (map[mutant.ID]string, error) {
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(max(1, c.settings.Workers))
	measured := map[string]bool{}
	for _, m := range mutants {
		project, found := c.projects.of(m.File)
		if found && !measured[project.folder] {
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
		project, found := c.projects.of(m.File)
		if !found {
			uncovered[m.ID] = fmt.Sprintf("no Mix project holds %s", m.File)
			continue
		}
		run, err := c.run(ctx, project)
		if err != nil {
			return nil, err
		}
		if run.noTests {
			uncovered[m.ID] = fmt.Sprintf("the Elixir project in %s has no tests", run.project.path)
			continue
		}
		file, found := run.files[m.File]
		if !found {
			uncovered[m.ID] = fmt.Sprintf("the tests of the Elixir project in %s do not compile %s", run.project.path, m.File)
			continue
		}
		if !file.called() {
			uncovered[m.ID] = fmt.Sprintf("no test calls the code of %s", m.File)
			continue
		}
		// a function clause that compiles has a counted line, so a clause with no counted line is not in the build
		function := source.functionLines(m.File, m.Start)
		if function != nil && !file.counted(function) {
			uncovered[m.ID] = ""
			continue
		}
		functionRan := function == nil || file.ran(function)
		// :cover counts the line of a clause head for the body of the clause, so a guard that runs can sit on a line with a count of 0
		if head, found := source.clauseHeadOf(m.File, m.Start); found {
			notRun := !file.ran(head.lines)
			if !head.functionHead {
				notRun = !functionRan || file.counted(head.lines) && notRun
			}
			if notRun {
				uncovered[m.ID] = ""
			}
			continue
		}
		if _, counted := slices.BinarySearch(file.lines, m.Line); counted {
			if file.missing[m.Line] {
				uncovered[m.ID] = ""
			}
			continue
		}
		// :cover counts no line for a literal that ends a clause, or for a tuple of two elements
		if !functionRan {
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
	if err := os.WriteFile(filepath.Join(folder, taskFileName), task, 0o600); err != nil {
		return err
	}

	compile := process.Command{Program: "mix", Arguments: []string{"compile"}, Folder: run.project.folder, Env: testEnv(folder)}
	compiled, err := compile.Run(ctx)
	if err != nil {
		return fmt.Errorf("start mix in the Elixir project in %s: %w", run.project.path, err)
	}
	if compiled.Code != 0 {
		return fmt.Errorf("the Elixir project in %s does not compile:\n%s", run.project.path, strings.TrimSpace(compiled.Tail))
	}

	output := filepath.Join(folder, coverageFileName)
	test := process.Command{
		Program:   "elixir",
		Arguments: testArguments(folder),
		Folder:    run.project.folder,
		Env:       append(testEnv(folder), "MUTANTS_COVERAGE="+output),
	}
	started := time.Now()
	tested, err := test.Run(ctx)
	run.baseline = time.Since(started)
	if err != nil {
		return fmt.Errorf("start the tests of the Elixir project in %s: %w", run.project.path, err)
	}
	switch {
	case tested.Code == mixTestsFailed:
		return &language.TestsFailWithRealCode{Tests: "the Elixir project in " + run.project.path, Output: strings.TrimSpace(tested.Tail)}
	case tested.Code != 0:
		return fmt.Errorf("mix test exited with code %d in the Elixir project in %s:\n%s", tested.Code, run.project.path, strings.TrimSpace(tested.Tail))
	}

	var report struct {
		Tests int `json:"tests"`
		Files map[string]struct {
			Lines   []int `json:"lines"`
			Missing []int `json:"missing"`
		} `json:"files"`
	}
	content, err := os.ReadFile(output)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("the tests of the Elixir project in %s wrote no coverage:\n%s", run.project.path, strings.TrimSpace(tested.Tail))
	case err != nil:
		return err
	}
	if err := json.Unmarshal(content, &report); err != nil {
		return fmt.Errorf("read the coverage of the Elixir project in %s: %w", run.project.path, err)
	}
	run.noTests = report.Tests == 0
	run.files = map[string]coveredFile{}
	for file, measured := range report.Files {
		covered := coveredFile{lines: measured.Lines, missing: map[int]bool{}}
		slices.Sort(covered.lines)
		for _, line := range measured.Missing {
			covered.missing[line] = true
		}
		run.files[path.Join(run.project.path, filepath.ToSlash(file))] = covered
	}
	return nil
}

// --seed 0 turns off the random order of ExUnit, so that two runs give the same verdicts.
func testArguments(folder string) []string {
	return []string{"-r", filepath.Join(folder, taskFileName), "-S", "mix", "mutants.test", "--no-compile", "--seed", "0", "--no-color"}
}

func testEnv(folder string) []string {
	return append(os.Environ(),
		"MIX_ENV=test",
		"MUTANTS_FAILURES="+filepath.Join(folder, "failures"),
		"ERL_CRASH_DUMP="+filepath.Join(folder, "erl_crash.dump"),
	)
}
