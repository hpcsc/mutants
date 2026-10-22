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
	vms          *vms
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

	if r.vms != nil {
		// the build names each module by the real path of its file, and the VM finds the modules of the file by it
		real, err := filepath.EvalSymlinks(original)
		if err != nil {
			real = original
		}
		verdict, judged, err := r.inReusedVM(ctx, project, real, mutated, process.TestLimit(run.baseline))
		if err != nil || judged {
			return verdict, err
		}
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

// inReusedVM leaves a LIVED mutant of code that ran when the application started or a test file compiled to a new
// VM, because the reused VM did both before the mutant loaded.
func (r *runner) inReusedVM(ctx context.Context, project project, original, mutated string, limit time.Duration) (mutant.Verdict, bool, error) {
	first, judged, err := r.vms.test(ctx, project, original, mutated, limit)
	if err != nil || !judged || first.needsNewVM() {
		return mutant.Verdict{}, false, err
	}
	if !r.confirmKills {
		return first.verdict, true, nil
	}
	verdict, err := mutant.ConfirmKill(ctx, first.verdict, func(ctx context.Context) (mutant.Verdict, error) {
		again, judged, err := r.vms.test(ctx, project, original, mutated, limit)
		if !judged {
			return mutant.Verdict{Status: mutant.Killed}, err
		}
		return again.verdict, err
	})
	return verdict, true, err
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
