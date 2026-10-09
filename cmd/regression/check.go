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

func (r *commitResult) skip(text string) {
	r.Skips = append(r.Skips, text)
}

type checker struct {
	settings settings
	clone    *clone
	commit   string
	tester   tester
	result   commitResult
}

func newChecker(s settings, c *clone, commit string) *checker {
	return &checker{settings: s, clone: c, commit: commit, tester: newTester(c), result: commitResult{Repository: c.Name, Commit: commit}}
}

type mutantsRun struct {
	exit     int
	stderr   string
	duration time.Duration
	report   report
}

func (k *checker) check(ctx context.Context) (commitResult, error) {
	err := k.runChecks(ctx)
	return k.result, err
}

func (k *checker) runChecks(ctx context.Context) error {
	if err := k.clone.checkout(ctx, k.commit); err != nil {
		return err
	}
	first, err := k.runMutants(ctx)
	if err != nil {
		return err
	}
	k.result.Exit, k.result.Seconds, k.result.Mutants = first.exit, first.duration.Seconds(), first.report.Mutants
	switch first.exit {
	case 0, exitSurvivors:
	case exitLimit:
		k.result.Stop = "the run reached --limit " + k.settings.limit.String()
	case exitUsage:
		k.result.Stop = stopMessage(first.stderr)
		return k.checkStop(ctx)
	default:
		k.result.find("exit code", "", fmt.Sprintf("mutants run exited with code %d: %s", first.exit, stopMessage(first.stderr)))
		return nil
	}

	if k.settings.repeat && first.exit != exitLimit {
		second, err := k.runMutants(ctx)
		switch {
		case err != nil:
			return err
		case second.exit != first.exit && second.exit != 0 && second.exit != exitSurvivors:
			k.result.find("second run", "", fmt.Sprintf("exit %d in the first run, exit %d in the second: %s", first.exit, second.exit, stopMessage(second.stderr)))
		default:
			if err := k.compareRuns(ctx, first.report, second.report); err != nil {
				return err
			}
		}
	}
	if err := k.checkReruns(ctx); err != nil {
		return err
	}
	return k.checkPlainTests(ctx)
}

func (k *checker) runMutants(ctx context.Context) (mutantsRun, error) {
	before, err := k.clone.status(ctx)
	if err != nil {
		return mutantsRun{}, err
	}
	ran, leftover, err := command{
		program:   k.settings.mutants,
		arguments: []string{"run", "--base", k.commit + "^", "--format", "json", "--limit", k.settings.limit.String()},
		folder:    k.clone.folder,
		limit:     k.settings.limit + 5*time.Minute,
	}.runInSession(ctx)
	if err != nil {
		return mutantsRun{}, err
	}
	run := mutantsRun{exit: ran.code, stderr: ran.stderr, duration: ran.duration}
	if ran.timedOut {
		k.result.find("exit code", "", "mutants run did not stop at --limit "+k.settings.limit.String())
	}
	if mutantsPanicked(ran.stderr) {
		k.result.find("exit code", "", "mutants run panicked:\n"+ran.stderr)
	}
	after, err := k.clone.status(ctx)
	if err != nil {
		return mutantsRun{}, err
	}
	if after != before {
		k.result.find("work tree", "", fmt.Sprintf("git status before the run:\n%safter the run:\n%s", before, after))
	}
	for _, process := range leftover {
		k.result.find("processes", "", "alive after the run: "+process)
	}
	if ran.code == 0 || ran.code == exitSurvivors || ran.code == exitLimit {
		if err := json.Unmarshal([]byte(ran.stdout), &run.report); err != nil {
			k.result.find("exit code", "", "the JSON report does not parse: "+err.Error())
		}
	}
	return run, nil
}

func (k *checker) checkStop(ctx context.Context) error {
	var folder string
	if match := goStop.FindStringSubmatch(k.result.Stop); match != nil && k.clone.Language == "go" {
		listed, err := goCommand(k.clone.folder, "list", "-f", "{{.Dir}}", match[1]).run(ctx)
		if err != nil {
			return err
		}
		folder = strings.TrimSpace(listed.stdout)
	}
	if match := pythonStop.FindStringSubmatch(k.result.Stop); match != nil {
		folder = filepath.Join(k.clone.folder, match[1])
	}
	if folder == "" {
		k.result.find("exit code", "", "mutants run exited with code 2: "+k.result.Stop)
		return nil
	}
	err := k.tester.baseline(ctx, folder)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		k.result.skip(err.Error())
		return nil
	}
	text := "mutants says that the tests fail with the real code, but a plain run of them passes: " + firstLine(k.result.Stop)
	passes, runs, err := k.countPasses(1, 1, func() (plainResult, error) { return k.tester.testIn(ctx, folder) })
	switch {
	case err != nil:
		return err
	case passes < runs:
		k.result.skip(fmt.Sprintf("%s, and the plain tests pass in %d of %d runs with the real code", text, passes, runs))
	default:
		k.result.find("plain tests", "", text)
	}
	return nil
}

func (k *checker) compareRuns(ctx context.Context, first, second report) error {
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
			if err := k.findUnlessFlaky(ctx, m, "second run", fmt.Sprintf("%s in the first run, %q in the second", m.Status, status)); err != nil {
				return err
			}
		}
	}
	for _, m := range second.Mutants {
		if _, found := firstStatuses[m.ID]; !found {
			k.result.find("second run", m.ID, "only in the second run, with "+m.Status)
		}
	}
	return nil
}

func (k *checker) checkReruns(ctx context.Context) error {
	for _, status := range []string{killed, lived, notCovered, notViable} {
		for _, m := range sample(k.result.Mutants, status, k.settings.sample) {
			ran, leftover, err := command{
				program:   k.settings.mutants,
				arguments: []string{"rerun", "--base", k.commit + "^", m.ID},
				folder:    k.clone.folder,
				limit:     plainLimit,
			}.runInSession(ctx)
			if err != nil {
				return err
			}
			k.result.Reruns++
			for _, process := range leftover {
				k.result.find("processes", m.ID, "alive after the rerun: "+process)
			}
			if got := statusOfRerun(ran.stdout); got != m.Status || ran.code != rerunExit(m.Status) {
				text := fmt.Sprintf("%s in the run, but rerun exits %d with %q: %s", m.Status, ran.code, got, firstLine(ran.output()))
				if err := k.findUnlessFlaky(ctx, m, "rerun", text); err != nil {
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

func (k *checker) checkPlainTests(ctx context.Context) error {
	for _, status := range []string{lived, killed, notCovered} {
		for _, m := range sample(k.result.Mutants, status, k.settings.sample) {
			if err := k.checkPlainTest(ctx, m); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k *checker) checkPlainTest(ctx context.Context, m reportedMutant) error {
	source, err := os.ReadFile(filepath.Join(k.clone.folder, m.File))
	if err != nil {
		return err
	}
	if _, err := m.applyTo(source); err != nil {
		k.result.find("plain tests", m.ID, "the report gives a wrong place: "+err.Error())
		return nil
	}
	if err := k.tester.baseline(ctx, k.tester.folderOf(m.File)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		k.result.skip(m.ID + ": " + err.Error())
		return nil
	}

	var own, callers plainResult
	err = k.withMutant(m, func() error {
		var err error
		if own, err = k.tester.test(ctx, m.File); err != nil || m.Status != killed || !own.passed() {
			return err
		}
		callers, err = k.tester.testCallers(ctx, m.File)
		return err
	})
	if err != nil {
		return err
	}
	k.result.PlainTests++
	switch {
	case own.timedOut:
		k.result.skip(m.ID + ": the plain tests ran past " + plainLimit.String())
	case strings.Contains(own.notBuilt, "declared and not used") || strings.Contains(own.notBuilt, "imported and not used") || strings.Contains(own.notBuilt, "defined and not used"):
		k.result.skip(m.ID + ": the mutant builds only after mutants repairs the names that it leaves unused")
	case own.notBuilt != "" && m.Status != notCovered:
		k.result.find("plain tests", m.ID, m.Status+", but the mutant does not build: "+firstLine(own.notBuilt))
	case m.Status == killed && own.failure == "" && callers.failure == "":
		return k.findUnlessFlaky(ctx, m, "plain tests", "KILLED, but each plain test passes")
	case m.Status != killed && own.failure != "":
		return k.findUnlessFlaky(ctx, m, "plain tests", m.Status+", but a plain test fails: "+own.failure)
	}
	return nil
}

func (k *checker) findUnlessFlaky(ctx context.Context, m reportedMutant, check, text string) error {
	source, err := os.ReadFile(filepath.Join(k.clone.folder, m.File))
	if err != nil {
		return err
	}
	if _, err := m.applyTo(source); err != nil {
		k.result.find(check, m.ID, text)
		return nil
	}
	if err := k.tester.baseline(ctx, k.tester.folderOf(m.File)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		k.result.find(check, m.ID, text)
		return nil
	}
	var passes, runs int
	err = k.withMutant(m, func() error {
		var err error
		passes, runs, err = k.countPasses(0, 0, func() (plainResult, error) { return k.tester.test(ctx, m.File) })
		return err
	})
	switch {
	case err != nil:
		return err
	case passes > 0 && passes < runs:
		k.result.skip(fmt.Sprintf("%s: %s, and the plain tests pass in %d of %d runs with the mutant", m.ID, text, passes, runs))
	default:
		k.result.find(check, m.ID, text)
	}
	return nil
}

func (k *checker) countPasses(passes, runs int, run func() (plainResult, error)) (int, int, error) {
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

func (k *checker) withMutant(m reportedMutant, do func() error) error {
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

func mutantsPanicked(stderr string) bool {
	return strings.Contains(stderr, "\ngoroutine ") && strings.Contains(stderr, "github.com/hpcsc/mutants/internal/")
}
