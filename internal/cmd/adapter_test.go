//go:build unit

package cmd

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAdapter(t *testing.T) {
	run := func(t *testing.T, action func(context.Context, *cli.Command) error, arguments ...string) error {
		t.Helper()
		command := &cli.Command{Name: "test", Action: action, Writer: io.Discard, ErrWriter: io.Discard}

		return command.Run(context.Background(), append([]string{"test"}, arguments...))
	}

	t.Run("no arguments", func(t *testing.T) {
		t.Run("runs the action, whatever the arguments are", func(t *testing.T) {
			invoked := false
			action := asNoArgumentsAction(func(context.Context, *cli.Command) error {
				invoked = true
				return nil
			})

			err := run(t, action, "argument-1", "argument-2")

			require.NoError(t, err)
			require.True(t, invoked, "adapted action was not invoked")
		})
	})

	t.Run("one argument", func(t *testing.T) {
		t.Run("no argument returns the validation message", func(t *testing.T) {
			action := asOneArgumentAction(func(context.Context, *cli.Command, string) error {
				require.Fail(t, "should not be called when error happens")
				return nil
			}, "one argument is required")

			err := run(t, action)

			require.ErrorContains(t, err, "one argument is required")
		})

		t.Run("one argument reaches the action", func(t *testing.T) {
			action := asOneArgumentAction(func(_ context.Context, _ *cli.Command, argument string) error {
				require.Equal(t, "argument-1", argument)
				return nil
			}, "one argument is required")

			err := run(t, action, "argument-1")

			require.NoError(t, err)
		})

		t.Run("more arguments give the action the first one only", func(t *testing.T) {
			action := asOneArgumentAction(func(_ context.Context, _ *cli.Command, argument string) error {
				require.Equal(t, "argument-1", argument)
				return nil
			}, "one argument is required")

			err := run(t, action, "argument-1", "argument-2", "argument-3")

			require.NoError(t, err)
		})
	})

	t.Run("two arguments", func(t *testing.T) {
		t.Run("no argument returns the validation message", func(t *testing.T) {
			action := asTwoArgumentsAction(func(context.Context, *cli.Command, string, string) error {
				require.Fail(t, "should not be called when error happens")
				return nil
			}, "two arguments are required")

			err := run(t, action)

			require.ErrorContains(t, err, "two arguments are required")
		})

		t.Run("one argument returns the validation message", func(t *testing.T) {
			action := asTwoArgumentsAction(func(context.Context, *cli.Command, string, string) error {
				require.Fail(t, "should not be called when error happens")
				return nil
			}, "two arguments are required")

			err := run(t, action, "argument-1")

			require.ErrorContains(t, err, "two arguments are required")
		})

		t.Run("two arguments reach the action", func(t *testing.T) {
			action := asTwoArgumentsAction(func(_ context.Context, _ *cli.Command, first string, second string) error {
				require.Equal(t, "argument-1", first)
				require.Equal(t, "argument-2", second)
				return nil
			}, "two arguments are required")

			err := run(t, action, "argument-1", "argument-2")

			require.NoError(t, err)
		})

		t.Run("more arguments give the action the first two only", func(t *testing.T) {
			action := asTwoArgumentsAction(func(_ context.Context, _ *cli.Command, first string, second string) error {
				require.Equal(t, "argument-1", first)
				require.Equal(t, "argument-2", second)
				return nil
			}, "two arguments are required")

			err := run(t, action, "argument-1", "argument-2", "argument-3")

			require.NoError(t, err)
		})
	})

	t.Run("slice arguments", func(t *testing.T) {
		t.Run("no argument returns the validation message", func(t *testing.T) {
			action := asSliceArgumentsAction(func(context.Context, *cli.Command, []string) error {
				require.Fail(t, "should not be called when error happens")
				return nil
			}, "one argument is required")

			err := run(t, action)

			require.ErrorContains(t, err, "one argument is required")
		})

		t.Run("every argument reaches the action", func(t *testing.T) {
			action := asSliceArgumentsAction(func(_ context.Context, _ *cli.Command, arguments []string) error {
				require.Equal(t, []string{"argument-1", "argument-2", "argument-3"}, arguments)
				return nil
			}, "one argument is required")

			err := run(t, action, "argument-1", "argument-2", "argument-3")

			require.NoError(t, err)
		})
	})
}
