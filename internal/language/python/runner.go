package python

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/process"
)

const (
	baselineFactor = 3
	limitMargin    = 5 * time.Second
	// exitNotCompiled is the exit code of sitecustomize.py for a mutant that does not compile.
	exitNotCompiled = 97
	// maxTests keeps a command line below the limit of the operating system: with more tests, the runner gives
	// pytest their files.
	maxTests = 200
)

type runner struct {
	root     string
	projects *projects
	coverage *coverage
}

func (r *runner) Run(ctx context.Context, m mutant.Mutant) (mutant.Verdict, error) {
	project := r.projects.of(m.File)
	run, err := r.coverage.run(ctx, project)
	if err != nil {
		return mutant.Verdict{}, err
	}
	original := filepath.Join(r.root, m.File)
	content, err := mutate(original, m)
	if err != nil {
		return mutant.Verdict{Status: mutant.InfraError, Detail: err.Error()}, nil
	}

	folder, err := os.MkdirTemp("", "mutants-mutant-")
	if err != nil {
		return mutant.Verdict{}, err
	}
	defer os.RemoveAll(folder)
	if err := writeSupport(folder); err != nil {
		return mutant.Verdict{}, err
	}
	mutated := filepath.Join(folder, "mutant", filepath.Base(original))
	if err := os.MkdirAll(filepath.Dir(mutated), 0o700); err != nil {
		return mutant.Verdict{}, err
	}
	if err := os.WriteFile(mutated, []byte(content), 0o600); err != nil {
		return mutant.Verdict{}, err
	}

	tests := run.testsOf(m)
	limit := baselineFactor*run.duration(tests) + limitMargin
	tested, err := r.test(ctx, project, folder, original, mutated, tests, limit)
	// the load of the host can grow after the baseline, so a second run with twice the limit decides
	if err == nil && tested.TimedOut {
		limit *= 2
		tested, err = r.test(ctx, project, folder, original, mutated, tests, limit)
	}
	if err != nil {
		return mutant.Verdict{}, err
	}
	return verdict(tested, limit), nil
}

func (r *runner) test(ctx context.Context, project project, folder, original, mutated string, tests []string, limit time.Duration) (process.Exit, error) {
	python := r.projects.python(project)
	arguments := append(append(python[1:], pytestArguments()...), "-x")
	test := process.Command{
		Program:   python[0],
		Arguments: append(arguments, testArguments(tests)...),
		Folder:    project.folder,
		Env:       append(testEnv(folder), "MUTANTS_ORIGINAL="+original, "MUTANTS_MUTATED="+mutated),
		Limit:     limit,
	}
	return test.Run(ctx)
}

func testArguments(tests []string) []string {
	if len(tests) <= maxTests {
		return tests
	}
	var files []string
	for _, test := range tests {
		file, _, _ := strings.Cut(test, "::")
		if !slices.Contains(files, file) {
			files = append(files, file)
		}
	}
	return files
}

func mutate(path string, m mutant.Mutant) (string, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if m.Start < 0 || m.End > len(source) || string(source[m.Start:m.End]) != m.Original {
		return "", fmt.Errorf("%s changed after mutants read it", m.File)
	}
	return string(source[:m.Start]) + m.Replacement + string(source[m.End:]), nil
}

func verdict(tested process.Exit, limit time.Duration) mutant.Verdict {
	switch {
	case tested.TimedOut:
		return mutant.Verdict{Status: mutant.TimedOut, Detail: fmt.Sprintf("the tests ran past %s", limit)}
	case tested.Signaled:
		return mutant.Verdict{Status: mutant.InfraError, Detail: "a signal stopped the tests:\n" + strings.TrimSpace(tested.Tail)}
	case tested.Code == exitNotCompiled:
		return mutant.Verdict{Status: mutant.NotViable, Detail: strings.TrimSpace(tested.Tail)}
	case tested.Code == 0:
		return mutant.Verdict{Status: mutant.Lived}
	case tested.Code == pytestNoTests:
		return mutant.Verdict{Status: mutant.InfraError, Detail: fmt.Sprintf("pytest exited with code %d:\n%s", tested.Code, strings.TrimSpace(tested.Tail))}
	}
	// pytest also exits with 4, its code for a usage error, when a conftest.py fails to import, and the run
	// with the real code passed with the same arguments
	return mutant.Verdict{Status: mutant.Killed, Detail: failure(tested)}
}

func failure(tested process.Exit) string {
	var conftest, raised string
	for line := range strings.Lines(tested.Tail) {
		switch {
		case strings.HasPrefix(line, "FAILED ") || strings.HasPrefix(line, "ERROR "):
			return strings.TrimSpace(line)
		case strings.HasPrefix(line, "ImportError while loading conftest") && conftest == "":
			conftest = strings.TrimSuffix(strings.TrimSpace(line), ".")
		case strings.HasPrefix(line, "E "):
			raised = strings.TrimSpace(strings.TrimPrefix(line, "E "))
		}
	}
	if conftest != "" {
		return conftest + ": " + raised
	}
	return fmt.Sprintf("the tests exited with code %d", tested.Code)
}
