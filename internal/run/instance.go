package run

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
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

var ErrNothingToRun = errors.New("the run has no operator, no proposal and no check for caller gaps")

var errLimit = errors.New("the run reached its limit")

type Settings struct {
	Base              string
	Folders           []string
	Exclude           []string
	Operators         []string
	Workers           int
	Limit             time.Duration
	Proposals         []proposal.Proposal
	ProposalsAnywhere bool
	CallerGaps        bool
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

type Language struct {
	Adapter language.Adapter
	Pack    operator.Pack
}

func (l Language) takes(file string) bool {
	return slices.Contains(l.Adapter.Extensions(), filepath.Ext(file))
}

func (l Language) filesOf(files []string) []string {
	return slices.DeleteFunc(slices.Clone(files), func(file string) bool { return !l.takes(file) })
}

type Instance struct {
	repository *diff.Repository
	languages  []Language
	matcher    operator.Matcher
	stderr     io.Writer
	terminal   bool
}

func New(repository *diff.Repository, languages []Language, matcher operator.Matcher, stderr io.Writer, terminal bool) *Instance {
	return &Instance{repository: repository, languages: languages, matcher: matcher, stderr: stderr, terminal: terminal}
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
	languages, err := r.selected(settings.Operators)
	if err != nil {
		return Outcome{}, err
	}
	hasRules := slices.ContainsFunc(languages, func(l Language) bool { return len(l.Pack.Rules()) > 0 })
	if !hasRules && len(settings.Proposals) == 0 && !settings.CallerGaps {
		return Outcome{}, ErrNothingToRun
	}
	var outcome Outcome
	var lines diff.Lines
	if len(settings.Folders) > 0 {
		lines, err = r.repository.All(ctx, settings.Folders, diff.Pathspec{Extensions: r.extensions(), Exclude: settings.Exclude})
	} else {
		outcome.Base, err = r.repository.MergeBase(ctx, settings.Base)
		if err == nil {
			lines, err = r.changed(ctx, settings.Base, settings.Exclude)
		}
	}
	if err != nil {
		return Outcome{}, err
	}
	outcome.Files, outcome.Lines = len(lines.Files()), lines.Len()

	finding := progress.Start(r.stderr, r.terminal, fmt.Sprintf("Finding the mutants in %d files", outcome.Files))
	mutants, err := r.findAll(ctx, languages, lines.Files(), lines.Touches)
	if err == nil && len(settings.Proposals) > 0 {
		var proposed []mutant.Mutant
		var rejected []proposal.Rejection
		inScope := lines.Touches
		if settings.ProposalsAnywhere {
			inScope = func(string, int, int) bool { return true }
		}
		var accepted int
		if proposed, accepted, rejected, err = r.propose(ctx, settings.Proposals, inScope); err == nil {
			outcome.Proposals = &proposal.Summary{Accepted: accepted, Rejected: rejected}
		}
		mutants = append(mutants, proposed...)
		slices.SortStableFunc(mutants, byPosition)
	}
	finding.End()
	if err == nil && settings.CallerGaps {
		checking := progress.Start(r.stderr, r.terminal, "Running the tests of the changed callers")
		outcome.CallerGaps, err = r.callerGaps(ctx, lines)
		checking.End()
	}
	if err != nil || len(mutants) == 0 {
		return outcome, err
	}

	covering := progress.Start(r.stderr, r.terminal, "Running the tests one time with the real code")
	uncovered, err := r.uncovered(ctx, mutants, lines)
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

func (r *Instance) Rerun(ctx context.Context, id mutant.ID, base string, exclude []string) (mutant.Mutant, error) {
	unknown := fmt.Errorf("%w: %s", ErrUnknownID, id)
	l, found := r.languageOf(id.File)
	if !found {
		return mutant.Mutant{}, unknown
	}
	if _, err := os.Stat(filepath.Join(r.repository.Root(), id.File)); err != nil {
		return mutant.Mutant{}, unknown
	}
	var m mutant.Mutant
	var err error
	if id.Operator == proposal.Operator {
		m, err = r.findProposed(ctx, id)
	} else {
		m, err = r.findOperated(ctx, l, id)
	}
	if err != nil {
		return mutant.Mutant{}, err
	}

	changed, err := r.changed(ctx, base, exclude)
	if err != nil {
		fmt.Fprintf(r.stderr, "mutants runs no tests of a changed caller: %v\n", err)
	}
	uncovered, err := l.Adapter.Uncovered(ctx, []mutant.Mutant{m}, changed)
	if err != nil {
		return mutant.Mutant{}, err
	}
	if detail, found := uncovered[m.ID]; found {
		m.Verdict = mutant.Verdict{Status: mutant.NotCovered, Detail: detail}
		return m, nil
	}
	m.Verdict, err = l.Adapter.Runner().Run(ctx, m)
	return m, err
}

func (r *Instance) findOperated(ctx context.Context, l Language, id mutant.ID) (mutant.Mutant, error) {
	unknown := fmt.Errorf("%w: %s", ErrUnknownID, id)
	pack, err := l.Pack.Select([]string{id.Operator})
	if err != nil {
		return mutant.Mutant{}, unknown
	}
	l.Pack = pack
	everyLine := func(string, int, int) bool { return true }
	mutants, err := r.find(ctx, l, []string{id.File}, everyLine)
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

func (r *Instance) propose(ctx context.Context, proposals []proposal.Proposal, inScope func(file string, first, last int) bool) ([]mutant.Mutant, int, []proposal.Rejection, error) {
	sources := map[string][]byte{}
	saved := map[string]proposal.Proposal{}
	positions := map[string]int{}
	var mutants []mutant.Mutant
	var rejected []proposal.Rejection
	accepted := 0
	for _, p := range proposals {
		m, last, reason, err := r.proposed(p, sources)
		if err != nil {
			return nil, 0, nil, err
		}
		if reason == "" && !inScope(m.File, m.Line, last) {
			reason = "not on a changed line"
		}
		if reason != "" {
			rejected = append(rejected, proposal.Rejection{Proposal: p, Reason: reason})
			continue
		}
		accepted++
		position, same := positions[m.ID.String()]
		if !same {
			position = len(mutants)
			positions[m.ID.String()], saved[m.ID.String()] = position, p
			mutants = append(mutants, m)
		}
		if p.Ref != "" && !slices.Contains(mutants[position].Refs, p.Ref) {
			mutants[position].Refs = append(mutants[position].Refs, p.Ref)
		}
	}
	if len(saved) == 0 {
		return mutants, accepted, rejected, nil
	}
	store, err := r.store(ctx)
	if err == nil {
		err = store.Save(saved)
	}
	return mutants, accepted, rejected, err
}

func (r *Instance) proposed(p proposal.Proposal, sources map[string][]byte) (m mutant.Mutant, last int, reason string, err error) {
	file, reason := p.RepositoryFile()
	if reason != "" {
		return mutant.Mutant{}, 0, reason, nil
	}
	l, found := r.languageOf(file)
	if !found {
		return mutant.Mutant{}, 0, "no adapter of mutants takes this file", nil
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
	if !l.Adapter.Keep(edit) {
		return mutant.Mutant{}, 0, fmt.Sprintf("the %s adapter drops the edit, for example in a test file or in generated code", l.Adapter.Name()), nil
	}
	id := mutant.ID{File: file, Function: l.Adapter.Function(file, start), Operator: proposal.Operator, Number: p.Number()}
	m, last = r.mutantOf(l, edit, id, newLineOffsets(source))
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

func (r *Instance) findAll(ctx context.Context, languages []Language, files []string, inScope func(file string, first, last int) bool) ([]mutant.Mutant, error) {
	var mutants []mutant.Mutant
	for _, l := range languages {
		found, err := r.find(ctx, l, l.filesOf(files), inScope)
		if err != nil {
			return nil, err
		}
		mutants = append(mutants, found...)
	}
	slices.SortStableFunc(mutants, byPosition)
	return mutants, nil
}

func (r *Instance) find(ctx context.Context, l Language, files []string, inScope func(file string, first, last int) bool) ([]mutant.Mutant, error) {
	edits, err := l.Pack.Edits(ctx, r.matcher, r.repository.Root(), files)
	if err != nil {
		return nil, err
	}
	edits = slices.DeleteFunc(edits, func(edit operator.Edit) bool { return !l.Adapter.Keep(edit) })
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
		id := ids.Next(edit.File, l.Adapter.Function(edit.File, edit.Start), edit.Operator)
		lines, found := offsets[edit.File]
		if !found {
			source, err := os.ReadFile(filepath.Join(r.repository.Root(), edit.File))
			if err != nil {
				return nil, err
			}
			lines = newLineOffsets(source)
			offsets[edit.File] = lines
		}
		m, last := r.mutantOf(l, edit, id, lines)
		if inScope(edit.File, m.Line, last) {
			mutants = append(mutants, m)
		}
	}
	return mutants, nil
}

func (r *Instance) mutantOf(l Language, edit operator.Edit, id mutant.ID, lines lineOffsets) (m mutant.Mutant, last int) {
	line, column := lines.position(edit.Start)
	last, _ = lines.position(max(edit.Start, edit.End-1))
	endLine, endColumn := lines.position(edit.End)
	return mutant.Mutant{
		ID:          id,
		File:        edit.File,
		Language:    l.Adapter.Name(),
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
				l, _ := r.languageOf(mutants[i].File)
				result, err := l.Adapter.Runner().Run(groupContext, mutants[i])
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

func (r *Instance) selected(names []string) ([]Language, error) {
	packs := make([]operator.Pack, len(r.languages))
	for i, l := range r.languages {
		packs[i] = l.Pack
	}
	chosen, err := operator.Select(packs, names)
	if err != nil {
		return nil, err
	}
	languages := slices.Clone(r.languages)
	for i := range languages {
		languages[i].Pack = chosen[i]
	}
	return languages, nil
}

func (r *Instance) changed(ctx context.Context, base string, exclude []string) (diff.Lines, error) {
	var lines diff.Lines
	for _, indentationMatters := range []bool{false, true} {
		var extensions []string
		for _, l := range r.languages {
			if l.Adapter.IndentationMatters() == indentationMatters {
				extensions = append(extensions, l.Adapter.Extensions()...)
			}
		}
		if len(extensions) == 0 {
			continue
		}
		found, err := r.repository.Changed(ctx, base, diff.Pathspec{Extensions: extensions, Exclude: exclude, IgnoreSpaceChange: !indentationMatters})
		if err != nil {
			return diff.Lines{}, err
		}
		lines.AddAll(found)
	}
	return lines, nil
}

func (r *Instance) extensions() []string {
	var extensions []string
	for _, l := range r.languages {
		extensions = append(extensions, l.Adapter.Extensions()...)
	}
	return extensions
}

func (r *Instance) languageOf(file string) (Language, bool) {
	index := slices.IndexFunc(r.languages, func(l Language) bool { return l.takes(file) })
	if index < 0 {
		return Language{}, false
	}
	return r.languages[index], true
}

func (r *Instance) callerGaps(ctx context.Context, lines diff.Lines) (*[]language.CallerGap, error) {
	var gaps *[]language.CallerGap
	for _, l := range r.languages {
		finder, hasCheck := l.Adapter.(language.CallerGapFinder)
		changed := lines.WithExtensions(l.Adapter.Extensions())
		if !hasCheck || len(changed.Files()) == 0 {
			continue
		}
		found, err := finder.CallerGaps(ctx, changed)
		if err != nil {
			return nil, err
		}
		if gaps == nil {
			gaps = &[]language.CallerGap{}
		}
		*gaps = append(*gaps, found...)
	}
	return gaps, nil
}

func (r *Instance) uncovered(ctx context.Context, mutants []mutant.Mutant, changed diff.Lines) (map[mutant.ID]string, error) {
	uncovered := map[mutant.ID]string{}
	for _, l := range r.languages {
		own := slices.DeleteFunc(slices.Clone(mutants), func(m mutant.Mutant) bool { return !l.takes(m.File) })
		if len(own) == 0 {
			continue
		}
		found, err := l.Adapter.Uncovered(ctx, own, changed.WithExtensions(l.Adapter.Extensions()))
		if err != nil {
			return nil, err
		}
		maps.Copy(uncovered, found)
	}
	return uncovered, nil
}

func byPosition(a, b mutant.Mutant) int {
	return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Start, b.Start))
}
