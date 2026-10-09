package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func newMarker() string {
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	return "MUTANTS_REGRESSION_RUN=" + hex.EncodeToString(random)
}

func (c command) runInSession(ctx context.Context) (exit, []string, error) {
	marker := newMarker()
	if c.env == nil {
		c.env = os.Environ()
	}
	c.env = append(slices.Clone(c.env), marker)
	c.newSession = true
	ran, err := c.run(ctx)
	if err != nil {
		return ran, nil, err
	}
	leftover, err := killLeftoverProcesses(ctx, ran.pid, marker)
	return ran, leftover, err
}

// a test can start its own session, and macOS ps does not show the environment of a system program such as sleep
func killLeftoverProcesses(ctx context.Context, session int, marker string) ([]string, error) {
	var alive []int
	for range 3 {
		found, err := processesOf(session, marker)
		if err != nil || len(found) == 0 {
			return nil, err
		}
		alive = found
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	var killed []string
	for _, pid := range alive {
		killed = append(killed, strconv.Itoa(pid)+" "+commandOf(pid))
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return killed, nil
}

func processesOf(session int, marker string) ([]int, error) {
	marked, err := markerByProcess(marker)
	if err != nil {
		return nil, err
	}
	var found []int
	for pid, hasMarker := range marked {
		if sid, err := unix.Getsid(pid); hasMarker || (err == nil && sid == session) {
			found = append(found, pid)
		}
	}
	return found, nil
}

func markerByProcess(marker string) (map[int]bool, error) {
	marked := map[int]bool{}
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if pid, err := strconv.Atoi(entry.Name()); err == nil {
				environ, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
				marked[pid] = err == nil && bytes.Contains(environ, []byte(marker))
			}
		}
		return marked, nil
	}
	output, err := exec.Command("ps", "-axEww", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(string(output)) {
		field, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if pid, err := strconv.Atoi(field); err == nil {
			marked[pid] = strings.Contains(line, marker)
		}
	}
	return marked, nil
}

func commandOf(pid int) string {
	if runtime.GOOS == "linux" {
		output, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
		return strings.TrimSpace(strings.ReplaceAll(string(output), "\x00", " "))
	}
	output, _ := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return strings.TrimSpace(string(output))
}
