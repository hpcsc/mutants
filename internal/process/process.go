package process

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

type Command struct {
	Program   string
	Arguments []string
	Folder    string
	Env       []string
	Limit     time.Duration
}

type Exit struct {
	Code     int
	Signaled bool
	TimedOut bool
	Tail     string
}

// Run returns an error only when the program cannot start, or when ctx ends.
func (p Command) Run(ctx context.Context) (Exit, error) {
	output, err := os.CreateTemp("", "mutants-output-")
	if err != nil {
		return Exit{}, err
	}
	defer os.Remove(output.Name())
	defer output.Close()

	limited, cancel := ctx, context.CancelFunc(func() {})
	if p.Limit > 0 {
		limited, cancel = context.WithTimeout(ctx, p.Limit)
	}
	defer cancel()
	command := exec.CommandContext(limited, p.Program, p.Arguments...)
	command.Dir = p.Folder
	command.Env = p.Env
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
		return Exit{}, ctx.Err()
	}
	state := command.ProcessState
	if state == nil {
		return Exit{}, err
	}
	result := Exit{Code: state.ExitCode(), Tail: p.tail(output)}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		result.Signaled = true
		result.TimedOut = errors.Is(limited.Err(), context.DeadlineExceeded)
	}
	return result, nil
}

func (p Command) tail(output *os.File) string {
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
