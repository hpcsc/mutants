package golang

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const tailSize = 64 * 1024

type process struct {
	program   string
	arguments []string
	folder    string
	env       []string
	limit     time.Duration
}

type exit struct {
	code     int
	signaled bool
	timedOut bool
	tail     string
}

// run returns an error only when the program cannot start, or when ctx ends.
func (p process) run(ctx context.Context) (exit, error) {
	output, err := os.CreateTemp("", "mutants-output-")
	if err != nil {
		return exit{}, err
	}
	defer os.Remove(output.Name())
	defer output.Close()

	limited, cancel := ctx, context.CancelFunc(func() {})
	if p.limit > 0 {
		limited, cancel = context.WithTimeout(ctx, p.limit)
	}
	defer cancel()
	command := exec.CommandContext(limited, p.program, p.arguments...)
	command.Dir = p.folder
	command.Env = p.env
	command.Stdout = output
	command.Stderr = output
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = time.Second

	err = command.Run()
	// a process that the program starts can outlive it, also after a normal exit
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	if ctx.Err() != nil {
		return exit{}, ctx.Err()
	}
	state := command.ProcessState
	if state == nil {
		return exit{}, err
	}
	result := exit{code: state.ExitCode(), tail: p.tail(output)}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.signaled = true
		result.timedOut = errors.Is(limited.Err(), context.DeadlineExceeded)
	}
	return result, nil
}

func (p process) tail(output *os.File) string {
	info, err := output.Stat()
	if err != nil {
		return ""
	}
	if _, err := output.Seek(max(info.Size()-tailSize, 0), io.SeekStart); err != nil {
		return ""
	}
	text, _ := io.ReadAll(output)
	return string(text)
}
