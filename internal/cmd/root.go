package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/hpcsc/mutants/internal/version"
	"github.com/urfave/cli/v3"
)

const releaseRepository = "hpcsc/mutants"

var red = color.New(color.FgRed)

func Run(ctx context.Context) int {
	err := newCommand().Run(ctx, os.Args)
	var exit cli.ExitCoder
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		if message := err.Error(); message != "" {
			red.Fprintln(os.Stderr, message)
		}
		return exit.ExitCode()
	}
	red.Fprintln(os.Stderr, err.Error())
	return 1
}

func newCommand() *cli.Command {
	return &cli.Command{
		Name:                  "mutants",
		Version:               version.Current(),
		Usage:                 "find weak tests in the lines that a branch changes",
		EnableShellCompletion: true,
		// Run maps each error to its exit code, so the default handler must not call os.Exit
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{
			newRunCommand(),
			newRerunCommand(),
			newOperatorsCommand(),
			newConfigCommand(),
			newVersionCommand(),
			newUpdateCommand(),
		},
	}
}

func newVersionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "print the tag mutants was built from, or its commit when it has no tag",
		Action: func(_ context.Context, cmd *cli.Command) error {
			_, err := fmt.Fprintln(cmd.Root().Writer, version.Current())
			return err
		},
	}
}
