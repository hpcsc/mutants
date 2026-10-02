package run

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language"
	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/progress"
	"github.com/hpcsc/mutants/internal/proposal"
	"golang.org/x/sync/errgroup"
)

var ErrUnknownID = errors.New("no mutant has this id")

var ErrStaleProposal = errors.New("the proposal does not fit the code")

var errLimit = errors.New("the run reached its limit")

type Settings struct {
	Base       string
	Folders    []string
	Exclude    []string
	Operators  []string
	Workers    int
	Limit      time.Duration
	Proposals  []proposal.Proposal
	CallerGaps bool
}

// Proposals is nil when the run got no proposals, and CallerGaps is nil when the run did not look for caller
// gaps.
type Outcome struct {
	Base       string
	Files      int
	Lines      int
	Mutants    []mutant.Mutant
	Stopped    bool
	Proposals  *proposal.Summary
	CallerGaps *[]language.CallerGap
}

type Instance struct {
	repository *diff.Repository
	pack       operator.Pack
	matcher    operator.Matcher
	adapter    language.Adapter
	stderr     io.Writer
	terminal   bool
}

func New(repository *diff.Repository, pack operator.Pack, matcher operator.Matcher, adapter language.Adapter, stderr io.Writer, terminal bool) *Instance {
	return &Instance{repository: repository, pack: pack, matcher: matcher, adapter: adapter, stderr: stderr, terminal: terminal}
}

func (r *Instance) Run(ctx context.Context, settings Settings) (Outcome, error) {
	if settings.Limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, settings.Limit, errLimit)
		defer cancel()
	}
	outcome, err := r.run(ctx, settings)
	if errors.Is(context.Cause(ctx), errLimit) {
		outcome.Stopped = true
		return outcome, nil
	}
	return outcome, err
}

func (r *Instance) run(ctx context.Context, settings Settings) (Outcome, error) {
	pack, err := r.pack.Select(settings.Operators)
	if err != nil {
		return Outcome{}, err
	}
	pathspec := diff.Pathspec{Extensions: r.adapter.Extensions(), Exclude: settings.Exclude}
	var outcome Outcome
	var lines diff.Lines
	if len(settings.Folders) > 0 {
		lines, err = r.repository.All(ctx, settings.Folders, pathspec)
	} else {
		outcome.Base, err = r.repository.MergeBase(ctx, settings.Base)
		if err == nil {
			lines, err = r.repository.Changed(ctx, settings.Base, pathspec)
		}
	}
	if err != nil {
		return Outcome{}, err
	}
	outcome.Files, outcome.Lines = len(lines.Files()), lines.Len()

	finding := progress.Start(r.stderr, r.terminal, fmt.Sprintf("Finding the mutants in %d files", outcome.Files))
	mutants, err := r.find(ctx, pack, lines.Files(), lines.Touches)
	if err == nil && len(settings.Proposals) > 0 {
		var proposed []mutant.Mutant
		var rejected []proposal.Rejection
		if proposed, rejected, err = r.propose(ctx, settings.Proposals, lines.Touches); err == nil {
			outcome.Proposals = &proposal.Summary{Accepted: len(proposed), Rejected: rejected}
		}
		mutants = append(mutants, proposed...)
		slices.SortStableFunc(mutants, func(a, b mutant.Mutant) int {
			return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Start, b.Start))
		})
	}
	finding.End()
	if err == nil && settings.CallerGaps {
		checking := progress.Start(r.stderr, r.terminal, "Running the tests of the changed callers")
		var gaps []language.CallerGap
		if gaps, err = r.adapter.CallerGaps(ctx, lines); err == nil {
			outcome.CallerGaps = &gaps
		}
		checking.End()
	}
	if err != nil || len(mutants) == 0 {
		return outcome, err
	}

	covering := progress.Start(r.stderr, r.terminal, "Running the tests one time with the real code")
	uncovered, err := r.adapter.Uncovered(ctx, mutants)
	covering.End()
	if err != nil {
		return outcome, err
	}
	var toRun []int
	for i, m := range mutants {
		if detail, found := uncovered[m.ID]; found {
			mutants[i].Verdict = mutant.Verdict{Status: mutant.NotCovered, Detail: detail}
		} else {
			toRun = append(toRun, i)
		}
	}

	finished, err := r.test(ctx, mutants, toRun, settings.Workers)
	for i, m := range mutants {
		if _, found := uncovered[m.ID]; found || finished[i] {
			outcome.Mutants = append(outcome.Mutants, m)
		}
	}
	return outcome, err
}

func (r *Instance) Rerun(ctx context.Context, id mutant.ID) (mutant.Mutant, error) {
	unknown := fmt.Errorf("%w: %s", ErrUnknownID, id)
	if !slices.Contains(r.adapter.Extensions(), filepath.Ext(id.File)) {
		return mutant.Mutant{}, unknown
	}
	if _, err := os.Stat(filepath.Join(r.repository.Root(), id.File)); err != nil {
		return mutant.Mutant{}, unknown
	}
	find := r.findOperated
	if id.Operator == proposal.Operator {
		find = r.findProposed
	}
	m, err := find(ctx, id)
	if err != nil {
		return mutant.Mutant{}, err
	}

	uncovered, err := r.adapter.Uncovered(ctx, []mutant.Mutant{m})
	if err != nil {
		return mutant.Mutant{}, err
	}
	if detail, found := uncovered[m.ID]; found {
		m.Verdict = mutant.Verdict{Status: mutant.NotCovered, Detail: detail}
		return m, nil
	}
	m.Verdict, err = r.adapter.Runner().Run(ctx, m)
	return m, err
}

func (r *Instance) findOperated(ctx context.Context, id mutant.ID) (mutant.Mutant, error) {
	unknown := fmt.Errorf("%w: %s", ErrUnknownID, id)
	pack, err := r.pack.Select([]string{id.Operator})
	if err != nil {
		return mutant.Mutant{}, unknown
	}
	everyLine := func(string, int, int) bool { return true }
	mutants, err := r.find(ctx, pack, []string{id.File}, everyLine)
	if err != nil {
		return mutant.Mutant{}, err
	}
	index := slices.IndexFunc(mutants, func(m mutant.Mutant) bool { return m.ID == id })
	if index < 0 {
		return mutant.Mutant{}, unknown
	}
	return mutants[index], nil
}

func (r *Instance) findProposed(ctx context.Context, id mutant.ID) (mutant.Mutant, error) {
	store, err := r.store(ctx)
	if err != nil {
		return mutant.Mutant{}, err
	}
	saved, found, err := store.Find(id.String())
	switch {
	case err != nil:
		return mutant.Mutant{}, err
	case !found:
		return mutant.Mutant{}, fmt.Errorf("%w: %s", ErrUnknownID, id)
	}
	m, _, reason, err := r.proposed(saved, map[string][]byte{})
	switch {
	case err != nil:
		return mutant.Mutant{}, err
	case reason != "":
		return mutant.Mutant{}, fmt.Errorf("%w: %s", ErrStaleProposal, reason)
	}
	m.ID = id
	return m, nil
}

func (r *Instance) propose(ctx context.Context, proposals []proposal.Proposal, inScope func(file string, first, last int) bool) ([]mutant.Mutant, []proposal.Rejection, error) {
	sources := map[string][]byte{}
	accepted := map[string]proposal.Proposal{}
	var mutants []mutant.Mutant
	var rejected []proposal.Rejection
	for _, p := range proposals {
		m, last, reason, err := r.proposed(p, sources)
		if err != nil {
			return nil, nil, err
		}
		if _, same := accepted[m.ID.String()]; reason == "" && same {
			reason = "the same edit as another proposal"
		}
		if reason == "" && !inScope(m.File, m.Line, last) {
			reason = "not on a changed line"
		}
		if reason != "" {
			rejected = append(rejected, proposal.Rejection{Proposal: p, Reason: reason})
			continue
		}
		accepted[m.ID.String()] = p
		mutants = append(mutants, m)
	}
	if len(accepted) == 0 {
		return mutants, rejected, nil
	}
	store, err := r.store(ctx)
	if err == nil {
		err = store.Save(accepted)
	}
	return mutants, rejected, err
}

// proposed gives a reason when the proposal cannot become a mutant, and the last line that the edit changes
// when it can.
func (r *Instance) proposed(p proposal.Proposal, sources map[string][]byte) (mutant.Mutant, int, string, error) {
	file, reason := p.RepositoryFile()
	if reason != "" {
		return mutant.Mutant{}, 0, reason, nil
	}
	if !slices.Contains(r.adapter.Extensions(), filepath.Ext(file)) {
		return mutant.Mutant{}, 0, fmt.Sprintf("the %s adapter does not take this file", r.adapter.Name()), nil
	}
	source, found := sources[file]
	if !found {
		content, err := os.ReadFile(filepath.Join(r.repository.Root(), file))
		if errors.Is(err, os.ErrNotExist) {
			return mutant.Mutant{}, 0, "the file does not exist", nil
		}
		if err != nil {
			return mutant.Mutant{}, 0, "", err
		}
		source, sources[file] = content, content
	}
	start, reason := p.Start(source)
	if reason != "" {
		return mutant.Mutant{}, 0, reason, nil
	}
	edit := operator.Edit{
		File:        file,
		Operator:    proposal.Operator,
		Rule:        proposal.Operator,
		Start:       start,
		End:         start + len(p.Old),
		Original:    p.Old,
		Replacement: p.New,
	}
	if !r.adapter.Keep(edit) {
		return mutant.Mutant{}, 0, fmt.Sprintf("the %s adapter drops the edit, for example in a test file or in generated code", r.adapter.Name()), nil
	}
	id := mutant.ID{File: file, Function: r.adapter.Function(file, start), Operator: proposal.Operator, Number: p.Number()}
	m, last := r.mutantOf(edit, id, newLineOffsets(source))
	m.Bug = p.Bug
	return m, last, "", nil
}

func (r *Instance) store(ctx context.Context) (proposal.Store, error) {
	gitFolder, err := r.repository.GitFolder(ctx)
	if err != nil {
		return proposal.Store{}, err
	}
	return proposal.NewStore(filepath.Join(gitFolder, "mutants")), nil
}

func (r *Instance) find(ctx context.Context, pack operator.Pack, files []string, inScope func(file string, first, last int) bool) ([]mutant.Mutant, error) {
	edits, err := pack.Edits(ctx, r.matcher, r.repository.Root(), files)
	if err != nil {
		return nil, err
	}
	edits = slices.DeleteFunc(edits, func(edit operator.Edit) bool { return !r.adapter.Keep(edit) })
	slices.SortFunc(edits, func(a, b operator.Edit) int {
		return cmp.Or(
			strings.Compare(a.File, b.File), cmp.Compare(a.Start, b.Start), cmp.Compare(a.End, b.End),
			strings.Compare(a.Rule, b.Rule), strings.Compare(a.Replacement, b.Replacement),
		)
	})

	var ids mutant.Counter
	offsets := map[string]lineOffsets{}
	var mutants []mutant.Mutant
	for _, edit := range edits {
		id := ids.Next(edit.File, r.adapter.Function(edit.File, edit.Start), edit.Operator)
		lines, found := offsets[edit.File]
		if !found {
			source, err := os.ReadFile(filepath.Join(r.repository.Root(), edit.File))
			if err != nil {
				return nil, err
			}
			lines = newLineOffsets(source)
			offsets[edit.File] = lines
		}
		m, last := r.mutantOf(edit, id, lines)
		if inScope(edit.File, m.Line, last) {
			mutants = append(mutants, m)
		}
	}
	return mutants, nil
}

// mutantOf also gives the last line that the edit changes.
func (r *Instance) mutantOf(edit operator.Edit, id mutant.ID, lines lineOffsets) (mutant.Mutant, int) {
	line, column := lines.position(edit.Start)
	last, _ := lines.position(max(edit.Start, edit.End-1))
	endLine, endColumn := lines.position(edit.End)
	return mutant.Mutant{
		ID:          id,
		File:        edit.File,
		Line:        line,
		Column:      column,
		EndLine:     endLine,
		EndColumn:   endColumn,
		Start:       edit.Start,
		End:         edit.End,
		Operator:    edit.Operator,
		Original:    edit.Original,
		Replacement: edit.Replacement,
	}, last
}

// the workers take the mutants in file order, so the mutants of one package reuse its build cache
func (r *Instance) test(ctx context.Context, mutants []mutant.Mutant, indexes []int, workers int) (finished []bool, err error) {
	finished = make([]bool, len(mutants))
	if len(indexes) == 0 {
		return finished, nil
	}
	testing := progress.Start(r.stderr, r.terminal, fmt.Sprintf("Testing %d mutants", len(indexes)))
	defer testing.End()
	var mutex sync.Mutex
	done := 0

	jobs := make(chan int)
	group, groupContext := errgroup.WithContext(ctx)
	for range max(1, workers) {
		group.Go(func() error {
			for i := range jobs {
				result, err := r.adapter.Runner().Run(groupContext, mutants[i])
				if err != nil {
					return err
				}
				mutex.Lock()
				mutants[i].Verdict, finished[i] = result, true
				done++
				testing.Count(done, len(indexes))
				mutex.Unlock()
			}
			return nil
		})
	}
	group.Go(func() error {
		defer close(jobs)
		for _, i := range indexes {
			select {
			case jobs <- i:
			case <-groupContext.Done():
				return nil
			}
		}
		return nil
	})
	return finished, group.Wait()
}
