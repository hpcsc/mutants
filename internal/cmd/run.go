package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/hpcsc/mutants/internal/language/python"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/operator/astgrep"
	"github.com/hpcsc/mutants/internal/proposal"
	"github.com/hpcsc/mutants/internal/report"
	"github.com/hpcsc/mutants/internal/run"
	"github.com/urfave/cli/v3"
)

const (
	exitSurvivors   = 10
	exitUsage       = 2
	exitLimit       = 124
	exitInterrupted = 130
)

func newRunCommand() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "run the mutants of the lines that the branch changes",
		ArgsUsage: "[--all FOLDER...]",
		Description: "mutants run compares the work tree with the merge base of HEAD and --base, and runs the mutants of\n" +
			"the changed lines, committed or not. Every line of an untracked file counts.\n\n" +
			"Exit codes: 0 no survivor, 10 survivors or caller gaps, 124 the limit, 2 a usage or tool error.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "base", Usage: "compare with the merge base of HEAD and this commit (default: " + defaultBase + ")"},
			&cli.BoolFlag{Name: "all", Usage: "run every line of the files in each FOLDER, and in its subfolders for FOLDER/..."},
			&cli.IntFlag{Name: "workers", Value: defaultWorkers, Usage: "test this many mutants at once"},
			&cli.DurationFlag{Name: "limit", Usage: "stop the whole run after this time, and exit 124 (default: no limit)"},
			&cli.DurationFlag{Name: "build-limit", Value: 2 * time.Minute, Usage: "the least time for the build of one mutant; it is 3 times the build of the real code when that is longer"},
			&cli.StringSliceFlag{Name: "tags", Usage: "the build tags for go list, the coverage run and the builds"},
			&cli.StringSliceFlag{Name: "operators", Usage: "the operators to run: -NAME takes one out, +NAME adds one, NAME runs only the named ones, none runs no operator"},
			&cli.StringFlag{Name: "format", Value: "rows", Usage: "print rows or json"},
			&cli.StringFlag{Name: "json", Usage: "also write the JSON report to this file"},
			&cli.StringFlag{Name: "stryker", Usage: "also write the Stryker report to this file"},
			&cli.StringFlag{Name: "proposals", Usage: "also run the mutants that this file proposes, one JSON object on each line"},
			&cli.BoolFlag{Name: "proposals-anywhere", Usage: "accept a proposal also on a line that the diff does not change"},
			&cli.BoolFlag{Name: "caller-gaps", Usage: "also find the changed lines that the tests of their package run, but that no test of a changed caller runs"},
		},
		Action: runMutants,
	}
}

func runMutants(ctx context.Context, cmd *cli.Command) error {
	format := cmd.String("format")
	if format != "rows" && format != "json" {
		return cli.Exit(fmt.Sprintf("--format is rows or json, not %q", format), exitUsage)
	}
	folders := cmd.Args().Slice()
	if cmd.Bool("all") != (len(folders) > 0) {
		return cli.Exit("--all needs one FOLDER or more, and a FOLDER needs --all", exitUsage)
	}

	repository, err := diff.Open(ctx, ".")
	if err != nil {
		return cli.Exit(err, exitUsage)
	}
	configured, err := repositoryConfig(ctx, repository, cmd.Root().ErrWriter)
	if err != nil {
		return cli.Exit(err, exitUsage)
	}
	proposals, err := readProposals(cmd.String("proposals"))
	if err != nil {
		return cli.Exit(err, exitUsage)
	}
	if cmd.Bool("proposals-anywhere") && len(proposals) == 0 {
		return cli.Exit("--proposals-anywhere needs --proposals", exitUsage)
	}
	runSettings := run.Settings{
		Base:              configured.base(cmd),
		Folders:           folders,
		Exclude:           configured.Exclude,
		Operators:         configured.operators(cmd),
		Workers:           configured.workers(cmd),
		Limit:             cmd.Duration("limit"),
		Proposals:         proposals,
		ProposalsAnywhere: cmd.Bool("proposals-anywhere"),
		CallerGaps:        configured.callerGaps(cmd),
	}
	instance, err := newInstance(ctx, repository, golang.Settings{
		Tags:          configured.tags(cmd),
		BuildLimit:    cmd.Duration("build-limit"),
		Workers:       runSettings.Workers,
		ZeroFunctions: configured.Go.ZeroFunctions,
		ExcludeTypes:  configured.Go.ExcludeTypes,
	}, python.Settings{Command: configured.Python.Command, Workers: runSettings.Workers}, cmd.Root().ErrWriter)
	if err != nil {
		return cli.Exit(err, exitUsage)
	}

	outcome, err := instance.Run(ctx, runSettings)
	switch {
	case errors.Is(err, context.Canceled):
		return cli.Exit("mutants stopped: the run was interrupted", exitInterrupted)
	case err != nil:
		return cli.Exit(err, exitUsage)
	}
	if err := writeReports(cmd, repository.Root(), format, outcome); err != nil {
		return cli.Exit(err, exitUsage)
	}

	switch {
	case outcome.Stopped:
		return cli.Exit(fmt.Sprintf("mutants stopped at the limit of %s: the report holds the mutants that got a verdict", runSettings.Limit), exitLimit)
	case slices.ContainsFunc(outcome.Mutants, func(m mutant.Mutant) bool { return m.Verdict.Status.IsSurvivor() }),
		outcome.CallerGaps != nil && len(*outcome.CallerGaps) > 0:
		return cli.Exit("", exitSurvivors)
	}
	return nil
}

func newInstance(ctx context.Context, repository *diff.Repository, goSettings golang.Settings, pythonSettings python.Settings, status io.Writer) (*run.Instance, error) {
	if err := astgrep.CheckVersion(ctx); err != nil {
		return nil, err
	}
	if executable, err := os.Executable(); err == nil {
		goSettings.CacheProgram = []string{executable, "build-cache"}
	}
	languages, err := newLanguages(repository.Root(), goSettings, pythonSettings)
	if err != nil {
		return nil, err
	}
	return run.New(repository, languages, astgrep.New(repository.Root()), status, isTerminal(status)), nil
}

func newLanguages(root string, goSettings golang.Settings, pythonSettings python.Settings) ([]run.Language, error) {
	var languages []run.Language
	for _, adapter := range []language.Adapter{golang.New(root, goSettings), python.New(root, pythonSettings)} {
		pack, err := operator.Load(adapter.Name(), root)
		if err != nil {
			return nil, err
		}
		languages = append(languages, run.Language{Adapter: adapter, Pack: pack})
	}
	return languages, nil
}

func writeReports(cmd *cli.Command, root, format string, outcome run.Outcome) error {
	out := cmd.Root().Writer
	reported := report.Outcome{
		Base:       outcome.Base,
		Files:      outcome.Files,
		Lines:      outcome.Lines,
		Mutants:    outcome.Mutants,
		Stopped:    outcome.Stopped,
		Proposals:  outcome.Proposals,
		CallerGaps: outcome.CallerGaps,
	}
	if format == "json" {
		if err := report.NoMutantLine(cmd.Root().ErrWriter, reported); err != nil {
			return err
		}
		if err := report.JSON(out, reported); err != nil {
			return err
		}
	} else if err := report.Rows(out, reported); err != nil {
		return err
	}
	if path := cmd.String("json"); path != "" {
		if err := writeFile(path, func(w io.Writer) error { return report.JSON(w, reported) }); err != nil {
			return err
		}
	}
	if path := cmd.String("stryker"); path != "" {
		if err := writeFile(path, func(w io.Writer) error { return report.Stryker(w, root, outcome.Mutants) }); err != nil {
			return err
		}
	}
	return nil
}

func readProposals(path string) ([]proposal.Proposal, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the proposals: %w", err)
	}
	defer file.Close()
	proposals, err := proposal.Read(file)
	if err != nil {
		return nil, fmt.Errorf("read the proposals in %s: %w", path, err)
	}
	return proposals, nil
}

func writeFile(path string, write func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(file); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
