package elixir

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/process"
)

// exitNotCompiled is the exit code of mutants_task.exs for a mutant that does not compile.
const exitNotCompiled = 97

var failedTest = regexp.MustCompile(`^\s*\d+\) (.+)$`)

type runner struct {
	root         string
	projects     *projects
	coverage     *coverage
	confirmKills bool
}

func (r *runner) Run(ctx context.Context, m mutant.Mutant) (mutant.Verdict, error) {
	project, found := r.projects.of(m.File)
	if !found {
		return mutant.Verdict{Status: mutant.InfraError, Detail: fmt.Sprintf("no Mix project holds %s", m.File)}, nil
	}
	run, err := r.coverage.run(ctx, project)
	if err != nil {
		return mutant.Verdict{}, err
	}
	original := filepath.Join(r.root, m.File)
	source, err := os.ReadFile(original)
	if err != nil {
		return mutant.Verdict{Status: mutant.InfraError, Detail: err.Error()}, nil
	}
	content, err := m.Apply(source)
	if err != nil {
		return mutant.Verdict{Status: mutant.InfraError, Detail: err.Error()}, nil
	}

	folder, err := os.MkdirTemp("", "mutants-mutant-")
	if err != nil {
		return mutant.Verdict{}, err
	}
	defer os.RemoveAll(folder)
	if err := os.WriteFile(filepath.Join(folder, taskFileName), task, 0o600); err != nil {
		return mutant.Verdict{}, err
	}
	mutated := filepath.Join(folder, "mutant", filepath.Base(original))
	if err := os.MkdirAll(filepath.Dir(mutated), 0o700); err != nil {
		return mutant.Verdict{}, err
	}
	if err := os.WriteFile(mutated, []byte(content), 0o600); err != nil {
		return mutant.Verdict{}, err
	}

	test := process.Command{
		Program:   "elixir",
		Arguments: append(testArguments(folder), "--max-failures", "1"),
		Folder:    project.folder,
		Env:       append(testEnv(folder), "MUTANTS_ORIGINAL="+original, "MUTANTS_MUTATED="+mutated),
		Limit:     process.TestLimit(run.baseline),
	}
	tested, limit, err := test.RunAgainAfterTimeout(ctx)
	if err != nil {
		return mutant.Verdict{}, err
	}
	if !r.confirmKills {
		return verdict(tested, limit), nil
	}
	return mutant.ConfirmKill(ctx, verdict(tested, limit), func(ctx context.Context) (mutant.Verdict, error) {
		test.Limit = limit
		again, err := test.Run(ctx)
		return verdict(again, limit), err
	})
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
	}
	// the tests passed with the real code, so each other exit code comes from the mutant
	return mutant.Verdict{Status: mutant.Killed, Detail: failure(tested)}
}

func failure(tested process.Exit) string {
	lines := strings.Split(tested.Tail, "\n")
	for i, line := range lines {
		found := failedTest.FindStringSubmatch(line)
		if found == nil {
			continue
		}
		if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
			return found[1] + ", " + strings.TrimSpace(lines[i+1])
		}
		return found[1]
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "** (") {
			return strings.TrimSpace(line)
		}
	}
	return fmt.Sprintf("the tests exited with code %d", tested.Code)
}
