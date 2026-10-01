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
	"golang.org/x/sync/errgroup"
)

var ErrUnknownID = errors.New("no mutant has this id")

var errLimit = errors.New("the run reached its limit")

type Settings struct {
	Base      string
	Folders   []string
	Exclude   []string
	Operators []string
	Workers   int
	Limit     time.Duration
}

type Outcome struct {
	Base    string
	Files   int
	Lines   int
	Mutants []mutant.Mutant
	Stopped bool
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
	finding.End()
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
	m := mutants[index]

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
		line, column := lines.position(edit.Start)
		last, _ := lines.position(max(edit.Start, edit.End-1))
		if !inScope(edit.File, line, last) {
			continue
		}
		endLine, endColumn := lines.position(edit.End)
		mutants = append(mutants, mutant.Mutant{
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
		})
	}
	return mutants, nil
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
