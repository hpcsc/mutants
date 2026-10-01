package cmd

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"
)

// an adapter function that adapt a no-arguments function to CLI action handler
func asNoArgumentsAction(f func(context.Context, *cli.Command) error) func(context.Context, *cli.Command) error {
	return func(ctx context.Context, cmd *cli.Command) error {
		return f(ctx, cmd)
	}
}

func asOneArgumentAction(f func(context.Context, *cli.Command, string) error, validationMsg string) func(context.Context, *cli.Command) error {
	return func(ctx context.Context, cmd *cli.Command) error {
		argument := cmd.Args().First()
		if len(argument) == 0 {
			return errors.New(validationMsg)
		}

		return f(ctx, cmd, argument)
	}
}

func asTwoArgumentsAction(f func(context.Context, *cli.Command, string, string) error, validationMsg string) func(context.Context, *cli.Command) error {
	return func(ctx context.Context, cmd *cli.Command) error {
		arguments := cmd.Args().Slice()
		if len(arguments) < 2 {
			return errors.New(validationMsg)
		}

		return f(ctx, cmd, arguments[0], arguments[1])
	}
}

func asSliceArgumentsAction(f func(context.Context, *cli.Command, []string) error, validationMsg string) func(context.Context, *cli.Command) error {
	return func(ctx context.Context, cmd *cli.Command) error {
		arguments := cmd.Args().Slice()
		if len(arguments) == 0 {
			return errors.New(validationMsg)
		}

		return f(ctx, cmd, arguments)
	}
}
