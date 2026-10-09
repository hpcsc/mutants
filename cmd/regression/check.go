package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	goStop     = regexp.MustCompile(`the tests of (\S+) fail with the real code`)
	pythonStop = regexp.MustCompile(`the tests of the Python project in (\S+) fail with the real code`)
)

type finding struct {
	Check  string `json:"check"`
	Mutant string `json:"mutant,omitempty"`
	Text   string `json:"text"`
}

type commitResult struct {
	Repository string           `json:"repository"`
	Commit     string           `json:"commit"`
	Exit       int              `json:"exit"`
	Seconds    float64          `json:"seconds"`
	Stop       string           `json:"stop,omitempty"`
	Mutants    []reportedMutant `json:"mutants"`
	Reruns     int              `json:"reruns"`
	PlainTests int              `json:"plainTests"`
	Skips      []string         `json:"skips,omitempty"`
	Findings   []finding        `json:"findings,omitempty"`
}

func (r *commitResult) find(check, mutant, text string) {
	r.Findings = append(r.Findings, finding{Check: check, Mutant: mutant, Text: text})
}

type checker struct {
	settings settings
	clone    *clone
}

type mutantsRun struct {
	exit     int
	stderr   string
	duration time.Duration
	report   report
}

func (k checker) check(ctx context.Context, commit string) (commitResult, error) {
	result := commitResult{Repository: k.clone.Name, Commit: commit}
	if err := k.clone.checkout(ctx, commit); err != nil {
		return result, err
	}
	first, err := k.runMutants(ctx, commit, &result)
	if err != nil {
		return result, err
	}
	result.Exit, result.Seconds, result.Mutants = first.exit, first.duration.Seconds(), first.report.Mutants
	tester := newTester(k.clone)
	switch first.exit {
	case 0, exitSurvivors:
	case exitLimit:
		result.Stop = "the run reached --limit " + k.settings.limit.String()
	case exitUsage:
		result.Stop = stopMessage(first.stderr)
		return result, k.checkStop(ctx, tester, &result)
	default:
		result.find("exit code", "", fmt.Sprintf("mutants run exited with code %d: %s", first.exit, stopMessage(first.stderr)))
		return result, nil
	}

	if k.settings.repeat && first.exit != exitLimit {
		second, err := k.runMutants(ctx, commit, &result)
		switch {
		case err != nil:
			return result, err
		case second.exit != first.exit && second.exit != 0 && second.exit != exitSurvivors:
			result.find("second run", "", fmt.Sprintf("exit %d in the first run, exit %d in the second: %s", first.exit, second.exit, stopMessage(second.stderr)))
		default:
			if err := k.compareRuns(ctx, tester, first.report, second.report, &result); err != nil {
				return result, err
			}
		}
	}
	if err := k.checkReruns(ctx, tester, commit, &result); err != nil {
		return result, err
	}
	return result, k.checkPlainTests(ctx, tester, &result)
}

func (k checker) runMutants(ctx context.Context, commit string, result *commitResult) (mutantsRun, error) {
	before, err := k.clone.status(ctx)
	if err != nil {
		return mutantsRun{}, err
	}
	marker := newMarker()
	ran, err := command{
		program:    k.settings.mutants,
		arguments:  []string{"run", "--base", commit + "^", "--format", "json", "--limit", k.settings.limit.String()},
		folder:     k.clone.folder,
		env:        append(os.Environ(), marker),
		limit:      k.settings.limit + 5*time.Minute,
		newSession: true,
	}.run(ctx)
	if err != nil {
		return mutantsRun{}, err
	}
	run := mutantsRun{exit: ran.code, stderr: ran.stderr, duration: ran.duration}
	if ran.timedOut {
		result.find("exit code", "", "mutants run did not stop at --limit "+k.settings.limit.String())
	}
	if panicked(ran.stderr) {
		result.find("exit code", "", "mutants run panicked:\n"+ran.stderr)
	}
	after, err := k.clone.status(ctx)
	if err != nil {
		return mutantsRun{}, err
	}
	if after != before {
		result.find("work tree", "", fmt.Sprintf("git status before the run:\n%safter the run:\n%s", before, after))
	}
	leftover, err := killLeftoverProcesses(ctx, ran.pid, marker)
	if err != nil {
		return mutantsRun{}, err
	}
	for _, process := range leftover {
		result.find("processes", "", "alive after the run: "+process)
	}
	if ran.code == 0 || ran.code == exitSurvivors || ran.code == exitLimit {
		if err := json.Unmarshal([]byte(ran.stdout), &run.report); err != nil {
			result.find("exit code", "", "the JSON report does not parse: "+err.Error())
		}
	}
	return run, nil
}

func (k checker) checkStop(ctx context.Context, tester tester, result *commitResult) error {
	var folder string
	if match := goStop.FindStringSubmatch(result.Stop); match != nil && k.clone.Language == "go" {
		listed, err := goCommand(k.clone.folder, "list", "-f", "{{.Dir}}", match[1]).run(ctx)
		if err != nil {
			return err
		}
		folder = strings.TrimSpace(listed.stdout)
	}
	if match := pythonStop.FindStringSubmatch(result.Stop); match != nil {
		folder = filepath.Join(k.clone.folder, match[1])
	}
	if folder == "" {
		result.find("exit code", "", "mutants run exited with code 2: "+result.Stop)
		return nil
	}
	err := tester.baseline(ctx, folder)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		result.Skips = append(result.Skips, err.Error())
		return nil
	}
	text := "mutants says that the tests fail with the real code, but a plain run of them passes: " + firstLine(result.Stop)
	passes, runs, err := k.countPasses(1, 1, func() (plainResult, error) { return tester.testIn(ctx, folder) })
	switch {
	case err != nil:
		return err
	case passes < runs:
		result.Skips = append(result.Skips, fmt.Sprintf("%s, and the plain tests pass in %d of %d runs with the real code", text, passes, runs))
	default:
		result.find("plain tests", "", text)
	}
	return nil
}

func (k checker) compareRuns(ctx context.Context, tester tester, first, second report, result *commitResult) error {
	statusIn := func(r report) map[string]string {
		statuses := map[string]string{}
		for _, m := range r.Mutants {
			statuses[m.ID] = m.Status
		}
		return statuses
	}
	firstStatuses, secondStatuses := statusIn(first), statusIn(second)
	for _, m := range first.Mutants {
		if status := secondStatuses[m.ID]; status != m.Status {
			if err := k.findUnlessFlaky(ctx, tester, m, "second run", fmt.Sprintf("%s in the first run, %q in the second", m.Status, status), result); err != nil {
				return err
			}
		}
	}
	for _, m := range second.Mutants {
		if _, found := firstStatuses[m.ID]; !found {
			result.find("second run", m.ID, "only in the second run, with "+m.Status)
		}
	}
	return nil
}

func (k checker) checkReruns(ctx context.Context, tester tester, commit string, result *commitResult) error {
	for _, status := range []string{killed, lived, notCovered, notViable} {
		for _, m := range sample(result.Mutants, status, k.settings.sample) {
			marker := newMarker()
			ran, err := command{
				program:    k.settings.mutants,
				arguments:  []string{"rerun", "--base", commit + "^", m.ID},
				folder:     k.clone.folder,
				env:        append(os.Environ(), marker),
				limit:      plainLimit,
				newSession: true,
			}.run(ctx)
			if err != nil {
				return err
			}
			result.Reruns++
			leftover, err := killLeftoverProcesses(ctx, ran.pid, marker)
			if err != nil {
				return err
			}
			for _, process := range leftover {
				result.find("processes", m.ID, "alive after the rerun: "+process)
			}
			if got := statusOfRerun(ran.stdout); got != m.Status || ran.code != rerunExit(m.Status) {
				text := fmt.Sprintf("%s in the run, but rerun exits %d with %q: %s", m.Status, ran.code, got, firstLine(ran.output()))
				if err := k.findUnlessFlaky(ctx, tester, m, "rerun", text, result); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rerunExit(status string) int {
	switch status {
	case killed:
		return 0
	case lived, notCovered:
		return exitSurvivors
	}
	return exitNoVerdict
}

func (k checker) checkPlainTests(ctx context.Context, tester tester, result *commitResult) error {
	for _, status := range []string{lived, killed, notCovered} {
		for _, m := range sample(result.Mutants, status, k.settings.sample) {
			if err := k.checkPlainTest(ctx, tester, m, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k checker) checkPlainTest(ctx context.Context, tester tester, m reportedMutant, result *commitResult) error {
	source, err := os.ReadFile(filepath.Join(k.clone.folder, m.File))
	if err != nil {
		return err
	}
	if _, err := m.applyTo(source); err != nil {
		result.find("plain tests", m.ID, "the report gives a wrong place: "+err.Error())
		return nil
	}
	if err := tester.baseline(ctx, tester.folderOf(m.File)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result.Skips = append(result.Skips, m.ID+": "+err.Error())
		return nil
	}

	var own, callers plainResult
	err = k.withMutant(m, func() error {
		var err error
		if own, err = tester.test(ctx, m.File); err != nil || m.Status != killed || !own.passed() {
			return err
		}
		callers, err = tester.testCallers(ctx, m.File)
		return err
	})
	if err != nil {
		return err
	}
	result.PlainTests++
	switch {
	case own.timedOut:
		result.Skips = append(result.Skips, m.ID+": the plain tests ran past "+plainLimit.String())
	case strings.Contains(own.notBuilt, "declared and not used") || strings.Contains(own.notBuilt, "imported and not used"):
		result.Skips = append(result.Skips, m.ID+": the mutant builds only after mutants uses the names that it leaves unused")
	case own.notBuilt != "" && m.Status != notCovered:
		result.find("plain tests", m.ID, m.Status+", but the mutant does not build: "+firstLine(own.notBuilt))
	case m.Status == killed && own.failure == "" && callers.failure == "":
		return k.findUnlessFlaky(ctx, tester, m, "plain tests", "KILLED, but each plain test passes", result)
	case m.Status != killed && own.failure != "":
		return k.findUnlessFlaky(ctx, tester, m, "plain tests", m.Status+", but a plain test fails: "+own.failure, result)
	}
	return nil
}

// findUnlessFlaky runs the plain tests with the mutant again, because a test that depends on timing or on the
// order of the tests can kill a mutant in one run and let it live in the next.
func (k checker) findUnlessFlaky(ctx context.Context, tester tester, m reportedMutant, check, text string, result *commitResult) error {
	source, err := os.ReadFile(filepath.Join(k.clone.folder, m.File))
	if err != nil {
		return err
	}
	if _, err := m.applyTo(source); err != nil {
		result.find(check, m.ID, text)
		return nil
	}
	if err := tester.baseline(ctx, tester.folderOf(m.File)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result.find(check, m.ID, text)
		return nil
	}
	var passes, runs int
	err = k.withMutant(m, func() error {
		var err error
		passes, runs, err = k.countPasses(0, 0, func() (plainResult, error) { return tester.test(ctx, m.File) })
		return err
	})
	switch {
	case err != nil:
		return err
	case passes > 0 && passes < runs:
		result.Skips = append(result.Skips, fmt.Sprintf("%s: %s, and the plain tests pass in %d of %d runs with the mutant", m.ID, text, passes, runs))
	default:
		result.find(check, m.ID, text)
	}
	return nil
}

func (k checker) countPasses(passes, runs int, run func() (plainResult, error)) (int, int, error) {
	for runs < k.settings.plainRuns && (passes == 0 || passes == runs) {
		tested, err := run()
		if err != nil {
			return 0, 0, err
		}
		runs++
		if tested.passed() {
			passes++
		}
	}
	return passes, runs, nil
}

func (k checker) withMutant(m reportedMutant, do func() error) error {
	path := filepath.Join(k.clone.folder, m.File)
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mutated, err := m.applyTo(source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, mutated, 0o644); err != nil {
		return err
	}
	defer os.WriteFile(path, source, 0o644)
	return do()
}

func stopMessage(stderr string) string {
	var lines []string
	for line := range strings.Lines(stderr) {
		if !strings.HasSuffix(strings.TrimSpace(line), "…") {
			lines = append(lines, strings.TrimRight(line, "\n"))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// panicked tells a panic of mutants from a panic in the output of a test that mutants shows.
func panicked(stderr string) bool {
	return strings.Contains(stderr, "\ngoroutine ") && strings.Contains(stderr, "github.com/hpcsc/mutants/internal/")
}
