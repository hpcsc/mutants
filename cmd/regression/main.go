package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

const (
	exitNoVerdict = 1
	exitUsage     = 2
	exitSurvivors = 10
	exitLimit     = 124
)

type settings struct {
	repositories string
	work         string
	mutants      string
	commits      int
	sample       int
	plainRuns    int
	limit        time.Duration
	only         []string
	repeat       bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	s, err := parseSettings(arguments, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	repositories, err := readRepositories(s.repositories)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}

	var results []commitResult
	failed := false
	for _, r := range repositories {
		if len(s.only) > 0 && !slices.Contains(s.only, r.Name) {
			continue
		}
		checked, err := checkRepository(ctx, r, s, stdout)
		results = append(results, checked...)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", r.Name, err)
			failed = true
		}
		if ctx.Err() != nil {
			return exitUsage
		}
	}

	if err := summarize(stdout, results); err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	path := filepath.Join(s.work, "results.json")
	if err := writeResults(path, results); err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	fmt.Fprintf(stdout, "results: %s\n", path)
	if failed || slices.ContainsFunc(results, func(r commitResult) bool { return len(r.Findings) > 0 }) {
		return 1
	}
	return 0
}

func parseSettings(arguments []string, output io.Writer) (settings, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	var s settings
	var only string
	flags := flag.NewFlagSet("regression", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&s.repositories, "repositories", "cmd/regression/repositories.yml", "the file that lists the repositories")
	flags.StringVar(&s.work, "work", filepath.Join(cache, "mutants-regression"), "the folder for the clones and results.json")
	flags.StringVar(&s.mutants, "mutants", "bin/mutants", "the mutants binary to check")
	flags.IntVar(&s.commits, "commits", 5, "the number of newest commits to check in each repository")
	flags.IntVar(&s.sample, "sample", 3, "the number of mutants of each status for rerun and for the plain tests, in each commit")
	flags.IntVar(&s.plainRuns, "plain-runs", 20, "the most runs of the plain tests before a disagreement is a finding; they stop when one run passes and one fails")
	flags.DurationVar(&s.limit, "limit", 20*time.Minute, "the --limit of each mutants run")
	flags.StringVar(&only, "only", "", "check only these repositories, by name, split by commas")
	flags.BoolVar(&s.repeat, "repeat", true, "run mutants two times on each commit, and compare the verdicts")
	if err := flags.Parse(arguments); err != nil {
		return settings{}, err
	}
	if flags.NArg() > 0 {
		return settings{}, fmt.Errorf("regression takes no arguments, only flags: %s", strings.Join(flags.Args(), " "))
	}
	if s.plainRuns < 1 {
		return settings{}, fmt.Errorf("-plain-runs is 1 or more, not %d", s.plainRuns)
	}
	if only != "" {
		s.only = strings.Split(only, ",")
	}
	if s.mutants, err = filepath.Abs(s.mutants); err != nil {
		return settings{}, err
	}
	if _, err := os.Stat(s.mutants); err != nil {
		return settings{}, fmt.Errorf("build mutants first, for example with task build: %w", err)
	}
	return s, nil
}

func checkRepository(ctx context.Context, r repository, s settings, stdout io.Writer) ([]commitResult, error) {
	c, err := prepare(ctx, r, s.work, stdout)
	if err != nil {
		return nil, err
	}
	commits, err := c.sourceCommits(ctx, s.commits)
	if err != nil {
		return nil, err
	}
	var results []commitResult
	for _, commit := range commits {
		result, err := checker{settings: s, clone: c}.check(ctx, commit)
		results = append(results, result)
		printResult(stdout, result)
		if err != nil {
			return results, fmt.Errorf("%s: %w", commit[:10], err)
		}
	}
	return results, c.checkout(ctx, r.Commit)
}

func printResult(w io.Writer, r commitResult) {
	counts := map[string]int{}
	for _, m := range r.Mutants {
		counts[m.Status]++
	}
	var parts []string
	for _, status := range statuses {
		if counts[status] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", status, counts[status]))
		}
	}
	mutants := fmt.Sprintf("%d mutants", len(r.Mutants))
	if len(parts) > 0 {
		mutants += " (" + strings.Join(parts, ", ") + ")"
	}
	fmt.Fprintf(w, "%s %s: exit %d in %.0fs, %s, %d reruns, %d plain tests\n",
		r.Repository, r.Commit[:10], r.Exit, r.Seconds, mutants, r.Reruns, r.PlainTests)
	if r.Stop != "" {
		fmt.Fprintf(w, "  stop: %s\n", firstLine(r.Stop))
	}
	for _, skip := range r.Skips {
		fmt.Fprintf(w, "  skip: %s\n", skip)
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "  FINDING %s %s: %s\n", f.Check, f.Mutant, strings.ReplaceAll(f.Text, "\n", "\n    "))
	}
}

func summarize(w io.Writer, results []commitResult) error {
	byOperator := map[string]map[string]int{}
	var operators []string
	mutants, findings, stops := 0, 0, 0
	var seconds float64
	for _, r := range results {
		mutants += len(r.Mutants)
		findings += len(r.Findings)
		seconds += r.Seconds
		if r.Stop != "" {
			stops++
		}
		for _, m := range r.Mutants {
			if byOperator[m.Operator] == nil {
				byOperator[m.Operator] = map[string]int{}
				operators = append(operators, m.Operator)
			}
			byOperator[m.Operator][m.Status]++
		}
	}
	slices.Sort(operators)

	fmt.Fprintf(w, "\n%d commits, %d mutants, %d stops, %d findings, %s in the first runs of mutants\n",
		len(results), mutants, stops, findings, (time.Duration(seconds) * time.Second).String())
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintf(table, "operator\t%s\t\n", strings.Join(statuses, "\t"))
	for _, operator := range operators {
		row := []string{operator}
		for _, status := range statuses {
			row = append(row, fmt.Sprint(byOperator[operator][status]))
		}
		fmt.Fprintf(table, "%s\t\n", strings.Join(row, "\t"))
	}
	return table.Flush()
}

func writeResults(path string, results []commitResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}
