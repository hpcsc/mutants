//go:build unit

package process_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hpcsc/mutants/internal/process"
	"github.com/stretchr/testify/require"
)

func TestCommand(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		t.Run("gives the exit code and the output of the program", func(t *testing.T) {
			command := process.Command{Program: "sh", Arguments: []string{"-c", "echo out; echo err >&2; exit 3"}, Folder: t.TempDir()}

			exit, err := command.Run(context.Background())

			require.NoError(t, err)
			require.Equal(t, process.Exit{Code: 3, Tail: "out\nerr\n"}, exit)
		})

		t.Run("stops the program at its limit, and says that it ran past the limit", func(t *testing.T) {
			command := process.Command{Program: "sh", Arguments: []string{"-c", "sleep 30"}, Folder: t.TempDir(), Limit: 200 * time.Millisecond}

			exit, err := command.Run(context.Background())

			require.NoError(t, err)
			require.True(t, exit.TimedOut)
			require.True(t, exit.Signaled)
		})

		t.Run("stops a process that the program starts, also when the program ends by itself", func(t *testing.T) {
			command := process.Command{Program: "sh", Arguments: []string{"-c", "sleep 30 & echo $!"}, Folder: t.TempDir()}

			exit, err := command.Run(context.Background())

			require.NoError(t, err)
			child, err := strconv.Atoi(strings.TrimSpace(exit.Tail))
			require.NoError(t, err)
			require.Eventually(t, func() bool { return syscall.Kill(child, 0) != nil }, 5*time.Second, 50*time.Millisecond)
		})

		t.Run("an ended context stops the program and returns the error of the context", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			command := process.Command{Program: "sh", Arguments: []string{"-c", "sleep 30"}, Folder: t.TempDir()}

			_, err := command.Run(ctx)

			require.ErrorIs(t, err, context.DeadlineExceeded)
		})
	})

	t.Run("run again after timeout", func(t *testing.T) {
		t.Run("a program that ends within its limit runs one time", func(t *testing.T) {
			folder := t.TempDir()
			command := process.Command{Program: "sh", Arguments: []string{"-c", "echo run >> runs; exit 1"}, Folder: folder, Limit: 5 * time.Second}

			exit, limit, err := command.RunAgainAfterTimeout(context.Background())

			require.NoError(t, err)
			require.Equal(t, 1, exit.Code)
			require.Equal(t, 5*time.Second, limit)
			runs, err := os.ReadFile(filepath.Join(folder, "runs"))
			require.NoError(t, err)
			require.Equal(t, "run\n", string(runs))
		})

		t.Run("a program that runs past its limit runs a second time with twice the limit", func(t *testing.T) {
			command := process.Command{
				Program:   "sh",
				Arguments: []string{"-c", "if [ -f started ]; then exit 0; fi; touch started; sleep 30"},
				Folder:    t.TempDir(),
				Limit:     200 * time.Millisecond,
			}

			exit, limit, err := command.RunAgainAfterTimeout(context.Background())

			require.NoError(t, err)
			require.Equal(t, process.Exit{Code: 0}, exit)
			require.Equal(t, 400*time.Millisecond, limit)
		})

		t.Run("a program that runs past the limit of both runs timed out", func(t *testing.T) {
			command := process.Command{Program: "sh", Arguments: []string{"-c", "sleep 30"}, Folder: t.TempDir(), Limit: 100 * time.Millisecond}

			exit, limit, err := command.RunAgainAfterTimeout(context.Background())

			require.NoError(t, err)
			require.True(t, exit.TimedOut)
			require.Equal(t, 200*time.Millisecond, limit)
		})
	})

	t.Run("test limit", func(t *testing.T) {
		t.Run("is three times the baseline and five seconds more", func(t *testing.T) {
			require.Equal(t, 11*time.Second, process.TestLimit(2*time.Second))
		})
	})
}
