//go:build unit

package run_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/operator/astgrep"
	"github.com/hpcsc/mutants/internal/proposal"
	"github.com/hpcsc/mutants/internal/run"
	"github.com/stretchr/testify/require"
)

type fakeAdapter struct {
	root            string
	dropped         map[string]bool
	uncovered       map[string]string
	statuses        map[string]mutant.Status
	waits           map[string]bool
	failure         error
	callerGaps      []language.CallerGap
	callerGapsWaits bool
	mutex           sync.Mutex
	ran             []string
}

func (f *fakeAdapter) Name() string         { return "go" }
func (f *fakeAdapter) Extensions() []string { return []string{".go"} }
func (f *fakeAdapter) Keep(edit operator.Edit) bool {
	return !f.dropped[edit.Original+" -> "+edit.Replacement]
}
func (f *fakeAdapter) Runner() mutant.Runner { return f }

// Function names the func whose declaration comes last before offset, which is enough for the sources here.
func (f *fakeAdapter) Function(file string, offset int) string {
	source, err := os.ReadFile(filepath.Join(f.root, file))
	if err != nil || offset > len(source) {
		return ""
	}
	start := bytes.LastIndex(source[:offset], []byte("\nfunc "))
	if start < 0 {
		return ""
	}
	name, _, _ := strings.Cut(string(source[start+len("\nfunc "):]), "(")
	return name
}

func (f *fakeAdapter) Uncovered(_ context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error) {
	uncovered := map[mutant.ID]string{}
	for _, m := range mutants {
		if detail, found := f.uncovered[m.ID.String()]; found {
			uncovered[m.ID] = detail
		}
	}
	return uncovered, nil
}

func (f *fakeAdapter) CallerGaps(ctx context.Context, _ diff.Lines) ([]language.CallerGap, error) {
	if f.callerGapsWaits {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.callerGaps, nil
}

func (f *fakeAdapter) Run(ctx context.Context, m mutant.Mutant) (mutant.Verdict, error) {
	f.mutex.Lock()
	f.ran = append(f.ran, m.ID.String())
	f.mutex.Unlock()
	if f.waits[m.ID.String()] {
		<-ctx.Done()
		return mutant.Verdict{}, ctx.Err()
	}
	if f.failure != nil {
		return mutant.Verdict{}, f.failure
	}
	status, found := f.statuses[m.ID.String()]
	if !found {
		status = mutant.Killed
	}
	return mutant.Verdict{Status: status}, nil
}

type gitRepository struct {
	t    *testing.T
	root string
}

func newGitRepository(t *testing.T, files map[string]string) *gitRepository {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	r := &gitRepository{t: t, root: root}
	r.git("init", "--quiet", "--initial-branch=main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "test")
	r.git("config", "commit.gpgsign", "false")
	for name, content := range files {
		r.write(name, content)
	}
	r.git("add", "--all")
	r.git("commit", "--quiet", "--message", "start")
	return r
}

func (r *gitRepository) git(arguments ...string) {
	r.t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = r.root
	output, err := command.CombinedOutput()
	require.NoError(r.t, err, string(output))
}

func (r *gitRepository) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.root, name)
	require.NoError(r.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(r.t, os.WriteFile(path, []byte(content), 0o644))
}

func (r *gitRepository) head() string {
	r.t.Helper()
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = r.root
	output, err := command.Output()
	require.NoError(r.t, err)
	return strings.TrimSpace(string(output))
}

func (r *gitRepository) instance(adapter *fakeAdapter) *run.Instance {
	r.t.Helper()
	adapter.root = r.root
	repository, err := diff.Open(context.Background(), r.root)
	require.NoError(r.t, err)
	pack, err := operator.Load("go", r.root)
	require.NoError(r.t, err)
	return run.New(repository, pack, astgrep.New(r.root), adapter, io.Discard, false)
}

func idsAndStatuses(mutants []mutant.Mutant) []string {
	var rows []string
	for _, m := range mutants {
		rows = append(rows, m.ID.String()+" "+m.Verdict.Status.String())
	}
	return rows
}

const compareBefore = "package a\n\nfunc f(a, b int) bool {\n\tif a < b {\n\t\treturn true\n\t}\n\treturn false\n}\n"

const compareAfter = "package a\n\nfunc f(a, b int) bool {\n\tif a < b {\n\t\treturn true\n\t}\n\treturn a > b\n}\n"

var boundary = run.Settings{Base: "HEAD", Operators: []string{"CONDITIONALS_BOUNDARY"}, Workers: 2}

func TestInstance(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		t.Run("runs the mutants on the changed lines, and numbers them over the whole function", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			adapter := &fakeAdapter{statuses: map[string]mutant.Status{"a.go:f:CONDITIONALS_BOUNDARY#2": mutant.Lived}}

			outcome, err := r.instance(adapter).Run(context.Background(), boundary)

			require.NoError(t, err)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#2 LIVED"}, idsAndStatuses(outcome.Mutants))
			require.Equal(t, mutant.Mutant{
				ID:          mutant.ID{File: "a.go", Function: "f", Operator: "CONDITIONALS_BOUNDARY", Number: 2},
				File:        "a.go",
				Line:        7,
				Column:      9,
				EndLine:     7,
				EndColumn:   14,
				Start:       strings.Index(compareAfter, "a > b"),
				End:         strings.Index(compareAfter, "a > b") + len("a > b"),
				Operator:    "CONDITIONALS_BOUNDARY",
				Original:    "a > b",
				Replacement: "a >= b",
				Verdict:     mutant.Verdict{Status: mutant.Lived},
			}, outcome.Mutants[0])
			require.Equal(t, 1, outcome.Files)
			require.Equal(t, 1, outcome.Lines)
			require.Equal(t, r.head(), outcome.Base)
		})

		t.Run("an edit that the adapter drops gets no number", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			adapter := &fakeAdapter{dropped: map[string]bool{"a < b -> a <= b": true}}

			outcome, err := r.instance(adapter).Run(context.Background(), boundary)

			require.NoError(t, err)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#1 KILLED"}, idsAndStatuses(outcome.Mutants))
		})

		t.Run("a mutant that no test runs is NOT COVERED, and does not run", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": "package a\n"})
			r.write("a.go", compareAfter)
			adapter := &fakeAdapter{uncovered: map[string]string{"a.go:f:CONDITIONALS_BOUNDARY#1": "package . has no test files"}}

			outcome, err := r.instance(adapter).Run(context.Background(), boundary)

			require.NoError(t, err)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#1 NOT COVERED", "a.go:f:CONDITIONALS_BOUNDARY#2 KILLED"}, idsAndStatuses(outcome.Mutants))
			require.Equal(t, "package . has no test files", outcome.Mutants[0].Verdict.Detail)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#2"}, adapter.ran)
		})

		t.Run("folders run every line of their files, whatever the diff is", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareAfter})
			t.Chdir(r.root)
			settings := boundary
			settings.Folders = []string{"."}

			outcome, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)

			require.NoError(t, err)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#1 KILLED", "a.go:f:CONDITIONALS_BOUNDARY#2 KILLED"}, idsAndStatuses(outcome.Mutants))
			require.Empty(t, outcome.Base)
		})

		t.Run("a change with no mutant gives the counts of the changed files and lines", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", "// Package a compares.\n// It has no mutant.\n"+compareBefore)
			r.write("b.go", "package a\n")

			outcome, err := r.instance(&fakeAdapter{}).Run(context.Background(), boundary)

			require.NoError(t, err)
			require.Empty(t, outcome.Mutants)
			require.Equal(t, 2, outcome.Files)
			require.Equal(t, 3, outcome.Lines)
		})

		t.Run("the limit stops the run, and the outcome keeps the mutants that got a verdict", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": "package a\n"})
			r.write("a.go", compareAfter)
			adapter := &fakeAdapter{waits: map[string]bool{"a.go:f:CONDITIONALS_BOUNDARY#2": true}}
			settings := boundary
			settings.Workers, settings.Limit = 1, 500*time.Millisecond

			outcome, err := r.instance(adapter).Run(context.Background(), settings)

			require.NoError(t, err)
			require.True(t, outcome.Stopped)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#1 KILLED"}, idsAndStatuses(outcome.Mutants))
		})

		t.Run("an error from the runner stops the run before the next mutant, and returns the error", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": "package a\n"})
			r.write("a.go", compareAfter)
			failing := errors.New("go test cannot start")
			adapter := &fakeAdapter{failure: failing}
			settings := boundary
			settings.Workers = 1

			_, err := r.instance(adapter).Run(context.Background(), settings)

			require.ErrorIs(t, err, failing)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#1"}, adapter.ran)
		})

		t.Run("an unknown operator returns an error that names it", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			settings := boundary
			settings.Operators = []string{"-NOT_AN_OPERATOR"}

			_, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)

			require.ErrorContains(t, err, "NOT_AN_OPERATOR")
		})
	})

	t.Run("no operator", func(t *testing.T) {
		t.Run("with proposals, runs only the proposals", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			proposed := proposal.Proposal{File: "a.go", Old: "return a > b", New: "return a >= b", Bug: "equal values count as greater"}
			settings := run.Settings{Base: "HEAD", Operators: []string{operator.None}, Workers: 2, Proposals: []proposal.Proposal{proposed}}

			outcome, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)

			require.NoError(t, err)
			require.Equal(t, []string{fmt.Sprintf("a.go:f:PROPOSED#%d KILLED", proposed.Number())}, idsAndStatuses(outcome.Mutants))
		})

		t.Run("with no proposal and no check for caller gaps, returns ErrNothingToRun", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)

			_, err := r.instance(&fakeAdapter{}).Run(context.Background(), run.Settings{Base: "HEAD", Operators: []string{operator.None}, Workers: 2})

			require.ErrorIs(t, err, run.ErrNothingToRun)
		})
	})

	t.Run("caller gaps", func(t *testing.T) {
		t.Run("with the setting, the outcome holds the caller gaps of the adapter", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			gaps := []language.CallerGap{{File: "a.go", Function: "f", Lines: []int{7}, Callers: []string{"b"}}}
			settings := boundary
			settings.CallerGaps = true

			outcome, err := r.instance(&fakeAdapter{callerGaps: gaps}).Run(context.Background(), settings)

			require.NoError(t, err)
			require.NotNil(t, outcome.CallerGaps)
			require.Equal(t, gaps, *outcome.CallerGaps)
		})

		t.Run("with the setting and no gap, the outcome says that the run looked for caller gaps", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			settings := boundary
			settings.CallerGaps = true

			outcome, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)

			require.NoError(t, err)
			require.NotNil(t, outcome.CallerGaps)
			require.Empty(t, *outcome.CallerGaps)
		})

		t.Run("a run that stops at the limit before the check ends gives no caller gaps", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			settings := boundary
			settings.CallerGaps, settings.Limit = true, 500*time.Millisecond

			outcome, err := r.instance(&fakeAdapter{callerGapsWaits: true}).Run(context.Background(), settings)

			require.NoError(t, err)
			require.True(t, outcome.Stopped)
			require.Nil(t, outcome.CallerGaps)
		})

		t.Run("without the setting, the run does not look for caller gaps", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			gaps := []language.CallerGap{{File: "a.go", Function: "f", Lines: []int{7}, Callers: []string{"b"}}}

			outcome, err := r.instance(&fakeAdapter{callerGaps: gaps}).Run(context.Background(), boundary)

			require.NoError(t, err)
			require.Nil(t, outcome.CallerGaps)
		})
	})

	t.Run("propose", func(t *testing.T) {
		t.Run("runs each proposal on a changed line, and rejects each other proposal with its reason", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore, "notes.txt": "notes\n"})
			r.write("a.go", compareAfter)
			proposed := proposal.Proposal{File: "a.go", Old: "return a > b", New: "return a >= b", Bug: "equal values count as greater"}
			settings := boundary
			settings.Proposals = []proposal.Proposal{
				proposed,
				{File: "a.go", Old: "return", New: "panic(1)\n\treturn", Bug: "found two times"},
				{File: "a.go", Old: "if a < b", New: "if a <= b", Bug: "not changed"},
				{File: "missing.go", Old: "a", New: "b", Bug: "no file"},
				{File: "notes.txt", Old: "notes", New: "", Bug: "not Go"},
				{File: "../a.go", Old: "a", New: "b", Bug: "outside"},
				{File: "a.go", Old: "return a > b", New: "return b < a", Bug: "dropped"},
			}
			id := fmt.Sprintf("a.go:f:PROPOSED#%d", proposed.Number())
			adapter := &fakeAdapter{statuses: map[string]mutant.Status{id: mutant.Lived}, dropped: map[string]bool{"return a > b -> return b < a": true}}

			outcome, err := r.instance(adapter).Run(context.Background(), settings)

			require.NoError(t, err)
			require.Equal(t, []string{id + " LIVED", "a.go:f:CONDITIONALS_BOUNDARY#2 KILLED"}, idsAndStatuses(outcome.Mutants))
			require.Equal(t, "equal values count as greater", outcome.Mutants[0].Bug)
			require.NotNil(t, outcome.Proposals)
			require.Equal(t, 1, outcome.Proposals.Accepted)
			var reasons []string
			for _, rejection := range outcome.Proposals.Rejected {
				reasons = append(reasons, rejection.Proposal.Bug+": "+rejection.Reason)
			}
			require.Equal(t, []string{
				"found two times: old found 2 times",
				"not changed: not on a changed line",
				"no file: the file does not exist",
				"not Go: the go adapter does not take this file",
				"outside: the file is not in the repository",
				"dropped: the go adapter drops the edit, for example in a test file or in generated code",
			}, reasons)
		})

		t.Run("makes one mutant of the proposals with the same edit, with the ref of each", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			first := proposal.Proposal{File: "a.go", Old: "return a > b", New: "return a >= b", Bug: "equal values count as greater", Ref: "finding-1"}
			second := first
			second.Bug, second.Ref = "the boundary moves", "finding-2"
			settings := run.Settings{Base: "HEAD", Operators: []string{operator.None}, Workers: 2, Proposals: []proposal.Proposal{first, second, first}}

			outcome, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)

			require.NoError(t, err)
			require.Equal(t, []string{fmt.Sprintf("a.go:f:PROPOSED#%d KILLED", first.Number())}, idsAndStatuses(outcome.Mutants))
			require.Equal(t, []string{"finding-1", "finding-2"}, outcome.Mutants[0].Refs)
			require.Equal(t, &proposal.Summary{Accepted: 3}, outcome.Proposals)
		})

		t.Run("a run without proposals gives no summary of proposals", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)

			outcome, err := r.instance(&fakeAdapter{}).Run(context.Background(), boundary)

			require.NoError(t, err)
			require.Nil(t, outcome.Proposals)
		})

		t.Run("with ProposalsAnywhere, accepts a proposal on a line that the diff does not change", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			unchanged := proposal.Proposal{File: "a.go", Old: "if a < b", New: "if a <= b", Bug: "equal values count as less"}
			settings := run.Settings{Base: "HEAD", Operators: []string{operator.None}, Workers: 2, Proposals: []proposal.Proposal{unchanged}, ProposalsAnywhere: true}

			outcome, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)

			require.NoError(t, err)
			require.Equal(t, []string{fmt.Sprintf("a.go:f:PROPOSED#%d KILLED", unchanged.Number())}, idsAndStatuses(outcome.Mutants))
			require.Equal(t, &proposal.Summary{Accepted: 1}, outcome.Proposals)
		})

		t.Run("rerun finds an accepted proposal by its id, also without the proposals", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			proposed := proposal.Proposal{File: "a.go", Old: "return a > b", New: "return a >= b", Bug: "equal values count as greater"}
			settings := boundary
			settings.Proposals = []proposal.Proposal{proposed}
			_, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)
			require.NoError(t, err)
			id := mutant.ID{File: "a.go", Function: "f", Operator: proposal.Operator, Number: proposed.Number()}

			m, err := r.instance(&fakeAdapter{statuses: map[string]mutant.Status{id.String(): mutant.Lived}}).Rerun(context.Background(), id)

			require.NoError(t, err)
			require.Equal(t, []string{id.String() + " LIVED"}, idsAndStatuses([]mutant.Mutant{m}))
			require.Equal(t, "equal values count as greater", m.Bug)
		})

		t.Run("rerun of a proposal whose old text left the code returns ErrStaleProposal with the reason", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareBefore})
			r.write("a.go", compareAfter)
			proposed := proposal.Proposal{File: "a.go", Old: "return a > b", New: "return a >= b", Bug: "equal values count as greater"}
			settings := boundary
			settings.Proposals = []proposal.Proposal{proposed}
			_, err := r.instance(&fakeAdapter{}).Run(context.Background(), settings)
			require.NoError(t, err)
			r.write("a.go", compareBefore)

			_, err = r.instance(&fakeAdapter{}).Rerun(context.Background(), mutant.ID{File: "a.go", Function: "f", Operator: proposal.Operator, Number: proposed.Number()})

			require.ErrorIs(t, err, run.ErrStaleProposal)
			require.ErrorContains(t, err, "old not found")
		})
	})

	t.Run("rerun", func(t *testing.T) {
		t.Run("runs the mutant with the id again, whatever the diff is", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareAfter})
			adapter := &fakeAdapter{statuses: map[string]mutant.Status{"a.go:f:CONDITIONALS_BOUNDARY#1": mutant.Lived}}

			m, err := r.instance(adapter).Rerun(context.Background(), mutant.ID{File: "a.go", Function: "f", Operator: "CONDITIONALS_BOUNDARY", Number: 1})

			require.NoError(t, err)
			require.Equal(t, "a < b -> a <= b", m.Original+" -> "+m.Replacement)
			require.Equal(t, mutant.Lived, m.Verdict.Status)
			require.Equal(t, []string{"a.go:f:CONDITIONALS_BOUNDARY#1"}, adapter.ran)
		})

		t.Run("finds the mutant by the same id after a function with the same operator lands above it", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareAfter})
			r.write("a.go", "package a\n\nfunc g(a, b int) bool {\n\treturn a < b\n}\n\n"+strings.TrimPrefix(compareAfter, "package a\n\n"))

			m, err := r.instance(&fakeAdapter{}).Rerun(context.Background(), mutant.ID{File: "a.go", Function: "f", Operator: "CONDITIONALS_BOUNDARY", Number: 1})

			require.NoError(t, err)
			require.Equal(t, 8, m.Line)
		})

		t.Run("a mutant that no test runs is NOT COVERED, and does not run", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareAfter})
			adapter := &fakeAdapter{uncovered: map[string]string{"a.go:f:CONDITIONALS_BOUNDARY#1": ""}}

			m, err := r.instance(adapter).Rerun(context.Background(), mutant.ID{File: "a.go", Function: "f", Operator: "CONDITIONALS_BOUNDARY", Number: 1})

			require.NoError(t, err)
			require.Equal(t, mutant.NotCovered, m.Verdict.Status)
			require.Empty(t, adapter.ran)
		})

		t.Run("finds a mutant of an operator that is off by default", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": "package a\n\nimport \"fmt\"\n\nfunc f(err error) error {\n\treturn fmt.Errorf(\"load: %w\", err)\n}\n"})

			m, err := r.instance(&fakeAdapter{}).Rerun(context.Background(), mutant.ID{File: "a.go", Function: "f", Operator: "ERRORF_WRAP", Number: 1})

			require.NoError(t, err)
			require.Equal(t, `fmt.Errorf("load: %v", err)`, m.Replacement)
		})

		t.Run("an id that names no mutant returns ErrUnknownID", func(t *testing.T) {
			r := newGitRepository(t, map[string]string{"a.go": compareAfter, "notes.md": "notes\n"})
			ids := []mutant.ID{
				{File: "a.go", Function: "f", Operator: "CONDITIONALS_BOUNDARY", Number: 3},
				{File: "a.go", Function: "g", Operator: "CONDITIONALS_BOUNDARY", Number: 1},
				{File: "a.go", Function: "f", Operator: "NOT_AN_OPERATOR", Number: 1},
				{File: "a.go", Function: "f", Operator: "PROPOSED", Number: 123456},
				{File: "missing.go", Function: "f", Operator: "CONDITIONALS_BOUNDARY", Number: 1},
				{File: "notes.md", Function: "f", Operator: "CONDITIONALS_BOUNDARY", Number: 1},
			}
			for _, id := range ids {
				_, err := r.instance(&fakeAdapter{}).Rerun(context.Background(), id)

				require.ErrorIs(t, err, run.ErrUnknownID, id.String())
			}
		})
	})
}
