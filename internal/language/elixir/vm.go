package elixir

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hpcsc/mutants/internal/mutant"
)

//go:embed mutants_worker.exs
var worker []byte

const (
	workerFileName = "mutants_worker.exs"
	replyPrefix    = "mutants-reply "
	startMargin    = time.Minute
)

type vmReply struct {
	Status            string `json:"status"`
	Detail            string `json:"detail"`
	Restart           bool   `json:"restart"`
	LoadedBeforeTests bool   `json:"loaded_before_tests"`
}

// loadedBeforeTests is true when a module of the file loaded while the application started or a test file
// compiled, so its code can run before the mutant loads.
type vmVerdict struct {
	verdict           mutant.Verdict
	loadedBeforeTests bool
}

func (v vmVerdict) needsNewVM() bool {
	return v.verdict.Status == mutant.Lived && v.loadedBeforeTests
}

// vm must test one mutant at a time, because the code server holds one version of a module.
type vm struct {
	command *exec.Cmd
	input   io.WriteCloser
	replies chan vmReply
}

type vms struct {
	mutex    sync.Mutex
	idle     map[string][]*vm
	unusable map[string]bool
}

func newVMs() *vms {
	return &vms{idle: map[string][]*vm{}, unusable: map[string]bool{}}
}

// test returns false when no VM could judge the mutant, and an error only when ctx ends.
func (p *vms) test(ctx context.Context, project project, original, mutated string, limit time.Duration) (vmVerdict, bool, error) {
	v := p.take(ctx, project, limit)
	if v == nil {
		return vmVerdict{}, false, ctx.Err()
	}
	reply, answered := v.test(ctx, original, mutated, limit)
	if ctx.Err() != nil || !answered {
		v.stop()
		return vmVerdict{}, false, ctx.Err()
	}
	var judged vmVerdict
	switch reply.Status {
	case "killed":
		judged = vmVerdict{verdict: mutant.Verdict{Status: mutant.Killed, Detail: reply.Detail}}
	case "lived":
		judged = vmVerdict{verdict: mutant.Verdict{Status: mutant.Lived}, loadedBeforeTests: reply.LoadedBeforeTests}
	case "not_compiled":
		judged = vmVerdict{verdict: mutant.Verdict{Status: mutant.NotViable, Detail: strings.TrimSpace(reply.Detail)}}
	default:
		v.stop()
		return vmVerdict{}, false, nil
	}
	// the new VM opens its own pool of database connections, and the run must hold no more VMs than workers
	if reply.Restart || judged.needsNewVM() {
		v.stop()
	} else {
		p.give(project, v)
	}
	return judged, true, nil
}

func (p *vms) take(ctx context.Context, project project, limit time.Duration) *vm {
	p.mutex.Lock()
	if p.unusable[project.folder] {
		p.mutex.Unlock()
		return nil
	}
	if idle := p.idle[project.folder]; len(idle) > 0 {
		v := idle[len(idle)-1]
		p.idle[project.folder] = idle[:len(idle)-1]
		p.mutex.Unlock()
		return v
	}
	p.mutex.Unlock()

	v := startVM(ctx, project, limit+startMargin)
	if v == nil && ctx.Err() == nil {
		p.mutex.Lock()
		p.unusable[project.folder] = true
		p.mutex.Unlock()
	}
	return v
}

func (p *vms) give(project project, v *vm) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.idle[project.folder] = append(p.idle[project.folder], v)
}

// startVM returns nil when the VM is not ready within limit, for example when a test fails in it.
func startVM(ctx context.Context, project project, limit time.Duration) *vm {
	folder, err := os.MkdirTemp("", "mutants-vm-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(folder)
	task := filepath.Join(folder, workerFileName)
	if err := os.WriteFile(task, worker, 0o600); err != nil {
		return nil
	}
	output, writer, err := os.Pipe()
	if err != nil {
		return nil
	}
	command := exec.Command("elixir", "-r", task, "-S", "mix", "mutants.worker", "--no-compile")
	command.Dir = project.folder
	// the default crash dump goes to the folder of the project, in the work tree
	command.Env = append(os.Environ(), "MIX_ENV=test", "ERL_CRASH_DUMP=/dev/null")
	command.Stdout, command.Stderr = writer, writer
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	input, err := command.StdinPipe()
	if err == nil {
		err = command.Start()
	}
	writer.Close()
	if err != nil {
		output.Close()
		return nil
	}
	v := &vm{command: command, input: input, replies: make(chan vmReply, 1)}
	go v.read(output)

	ready, answered := v.wait(ctx, limit)
	if !answered || ready.Status != "ready" {
		v.stop()
		return nil
	}
	return v
}

func (v *vm) read(output *os.File) {
	defer close(v.replies)
	defer output.Close()
	lines := bufio.NewScanner(output)
	lines.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for lines.Scan() {
		text, isReply := strings.CutPrefix(lines.Text(), replyPrefix)
		var reply vmReply
		if isReply && json.Unmarshal([]byte(text), &reply) == nil {
			v.replies <- reply
		}
	}
}

func (v *vm) test(ctx context.Context, original, mutated string, limit time.Duration) (vmReply, bool) {
	if _, err := io.WriteString(v.input, original+"\t"+mutated+"\n"); err != nil {
		return vmReply{}, false
	}
	return v.wait(ctx, limit)
}

func (v *vm) wait(ctx context.Context, limit time.Duration) (vmReply, bool) {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case reply, open := <-v.replies:
		return reply, open
	case <-timer.C:
		return vmReply{}, false
	case <-ctx.Done():
		return vmReply{}, false
	}
}

func (v *vm) stop() {
	_ = syscall.Kill(-v.command.Process.Pid, syscall.SIGKILL)
	v.input.Close()
	go func() { _ = v.command.Wait() }()
}
