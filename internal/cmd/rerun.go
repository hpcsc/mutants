package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/report"
	"github.com/hpcsc/mutants/internal/run"
	"github.com/urfave/cli/v3"
)

const exitNoVerdict = 1

func newRerunCommand() *cli.Command {
	return &cli.Command{
		Name:      "rerun",
		Usage:     "run one mutant again by its id, with no cache",
		ArgsUsage: "ID",
		Description: "The id is the text in [ ] at the end of a row, for example\n" +
			"internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1\n\n" +
			"Exit codes: 0 killed, 10 lived or not covered, 1 no verdict, 2 an unknown id.",
		Flags: []cli.Flag{
			&cli.DurationFlag{Name: "build-limit", Value: 2 * time.Minute, Usage: "stop the build of the mutant after this time"},
			&cli.StringSliceFlag{Name: "tags", Usage: "the build tags for go list, the coverage run and the build"},
		},
		OnUsageError: usageError,
		Action:       rerunMutant,
	}
}

func rerunMutant(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return cli.Exit("rerun needs one mutant id", exitUsage)
	}
	id, err := mutant.ParseID(cmd.Args().First())
	if err != nil {
		return cli.Exit(err, exitUsage)
	}
	repository, err := diff.Open(ctx, ".")
	if err != nil {
		return cli.Exit(err, exitNoVerdict)
	}
	configured, err := loadConfig(repository.Root())
	if err != nil {
		return cli.Exit(err, exitNoVerdict)
	}
	instance, err := newInstance(ctx, repository, golang.Settings{
		Tags:          configured.tags(cmd),
		BuildLimit:    cmd.Duration("build-limit"),
		Workers:       1,
		ZeroFunctions: configured.ZeroFunctions,
	}, cmd.Root().ErrWriter)
	if err != nil {
		return cli.Exit(err, exitNoVerdict)
	}

	m, err := instance.Rerun(ctx, id)
	switch {
	case errors.Is(err, run.ErrUnknownID):
		return cli.Exit(err, exitUsage)
	case errors.Is(err, context.Canceled):
		return cli.Exit("mutants stopped: the run was interrupted", exitInterrupted)
	case err != nil:
		return cli.Exit(err, exitNoVerdict)
	}

	row := fmt.Sprintf("%s: %s\n", m.Verdict.Status, report.Row(m))
	if m.Verdict.Detail != "" {
		row += "  " + strings.ReplaceAll(strings.TrimSpace(m.Verdict.Detail), "\n", "\n  ") + "\n"
	}
	if _, err := fmt.Fprint(cmd.Root().Writer, row); err != nil {
		return cli.Exit(err, exitNoVerdict)
	}
	switch {
	case m.Verdict.Status == mutant.Killed:
		return nil
	case m.Verdict.Status.IsSurvivor():
		return cli.Exit("", exitSurvivors)
	}
	return cli.Exit("", exitNoVerdict)
}
