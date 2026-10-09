package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type command struct {
	program    string
	arguments  []string
	folder     string
	env        []string
	limit      time.Duration
	newSession bool
}

type exit struct {
	pid      int
	code     int
	stdout   string
	stderr   string
	timedOut bool
	duration time.Duration
}

func (e exit) output() string {
	return e.stdout + e.stderr
}

// run returns an error only when the program cannot start, or when ctx ends.
func (c command) run(ctx context.Context) (exit, error) {
	limited, cancel := context.WithTimeout(ctx, c.limit)
	defer cancel()
	process := exec.CommandContext(limited, c.program, c.arguments...)
	process.Dir = c.folder
	process.Env = c.env
	var stdout, stderr bytes.Buffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: !c.newSession, Setsid: c.newSession}
	// mutants stops the tests of its mutants on SIGTERM, and a SIGKILL leaves them alive
	process.Cancel = func() error {
		return syscall.Kill(-process.Process.Pid, syscall.SIGTERM)
	}
	process.WaitDelay = 30 * time.Second

	started := time.Now()
	err := process.Run()
	result := exit{stdout: stdout.String(), stderr: stderr.String(), duration: time.Since(started)}
	if process.Process != nil {
		result.pid = process.Process.Pid
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.timedOut = limited.Err() != nil
	var exitError *exec.ExitError
	switch {
	case err == nil:
		return result, nil
	case errors.As(err, &exitError):
		result.code = exitError.ExitCode()
		return result, nil
	case result.timedOut:
		result.code = -1
		return result, nil
	}
	return result, err
}

func git(ctx context.Context, folder string, arguments ...string) (string, error) {
	result, err := command{program: "git", arguments: arguments, folder: folder, limit: 10 * time.Minute}.run(ctx)
	if err != nil {
		return "", err
	}
	if result.code != 0 {
		return "", errors.New("git " + strings.Join(arguments, " ") + ": " + strings.TrimSpace(result.output()))
	}
	return result.stdout, nil
}
