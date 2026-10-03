package golang

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hpcsc/mutants/internal/mutant"
	"github.com/hpcsc/mutants/internal/process"
	"golang.org/x/sync/errgroup"
)

type coverage struct {
	root     string
	settings Settings
	finder   *packageFinder
	mutex    sync.Mutex
	runs     map[string]*coverageRun
}

type coverageRun struct {
	once     sync.Once
	pkg      goPackage
	noTests  bool
	blocks   map[string][]block
	baseline baseline
	err      error
}

type baseline struct {
	build, test time.Duration
}

// block ends before endColumn, as in the Go coverage profile.
type block struct {
	startLine, startColumn int
	endLine, endColumn     int
	count                  int
}

func (b block) holds(line, column int) bool {
	afterStart := line > b.startLine || line == b.startLine && column >= b.startColumn
	beforeEnd := line < b.endLine || line == b.endLine && column < b.endColumn
	return afterStart && beforeEnd
}

func (b block) runs(line int) bool {
	return b.count > 0 && b.startLine <= line && line <= b.endLine
}

func newCoverage(root string, settings Settings, finder *packageFinder) *coverage {
	return &coverage{root: root, settings: settings, finder: finder, runs: map[string]*coverageRun{}}
}

// Go's profile has no block for the code after a function literal, so a mutant that no block holds runs.
func (c *coverage) uncovered(ctx context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error) {
	group, groupContext := errgroup.WithContext(ctx)
	group.SetLimit(max(1, c.settings.Workers))
	measured := map[string]bool{}
	for _, m := range mutants {
		folder := filepath.Dir(filepath.Join(c.root, m.File))
		if !measured[folder] {
			measured[folder] = true
			group.Go(func() error {
				_, err := c.run(groupContext, folder)
				return err
			})
		}
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	uncovered := map[mutant.ID]string{}
	for _, m := range mutants {
		run, err := c.run(ctx, filepath.Dir(filepath.Join(c.root, m.File)))
		if err != nil {
			return nil, err
		}
		if run.noTests {
			uncovered[m.ID] = fmt.Sprintf("package %s has no test files", filepath.ToSlash(filepath.Dir(m.File)))
			continue
		}
		heldByZero, lineRuns := false, false
		for _, b := range run.blocks[m.File] {
			if b.runs(m.Line) {
				lineRuns = true
			}
			if b.count == 0 && b.holds(m.Line, m.Column) {
				heldByZero = true
			}
		}
		if heldByZero && !lineRuns {
			uncovered[m.ID] = ""
		}
	}
	return uncovered, nil
}

func (c *coverage) baseline(ctx context.Context, folder string) (baseline, error) {
	run, err := c.run(ctx, folder)
	if err != nil {
		return baseline{}, err
	}
	return run.baseline, nil
}

func (c *coverage) run(ctx context.Context, folder string) (*coverageRun, error) {
	pkg, err := c.finder.find(ctx, folder)
	if err != nil {
		return nil, err
	}
	c.mutex.Lock()
	run, found := c.runs[pkg.Dir]
	if !found {
		run = &coverageRun{pkg: pkg}
		c.runs[pkg.Dir] = run
	}
	c.mutex.Unlock()
	run.once.Do(func() { run.err = c.measure(ctx, run) })
	return run, run.err
}

func (c *coverage) measure(ctx context.Context, run *coverageRun) error {
	measured, err := c.profile(ctx, run.pkg, []goPackage{run.pkg})
	run.noTests, run.blocks, run.baseline = measured.noTests, measured.blocks, measured.baseline
	return err
}

type profiled struct {
	noTests  bool
	blocks   map[string][]block
	baseline baseline
}

func (c *coverage) profile(ctx context.Context, pkg goPackage, covered []goPackage) (profiled, error) {
	var result profiled
	folder, err := os.MkdirTemp("", "mutants-coverage-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(folder)
	binary := filepath.Join(folder, "cover.test")
	arguments := []string{"test", "-c", "-cover", "-covermode=set"}
	folderOf := map[string]string{}
	var paths []string
	for _, p := range covered {
		folderOf[p.ImportPath] = p.Dir
		paths = append(paths, p.ImportPath)
	}
	if len(covered) != 1 || covered[0].ImportPath != pkg.ImportPath {
		arguments = append(arguments, "-coverpkg="+strings.Join(paths, ","))
	}
	build := process.Command{
		Program:   "go",
		Arguments: append(append(append(arguments, "-o", binary), c.settings.tagArguments()...), "."),
		Folder:    pkg.Dir,
		Env:       c.settings.buildEnv(),
	}
	started := time.Now()
	built, err := build.Run(ctx)
	result.baseline.build = time.Since(started)
	if err != nil {
		return result, err
	}
	if built.Code != 0 {
		return result, fmt.Errorf("build the tests of %s: %s", pkg.ImportPath, strings.TrimSpace(built.Tail))
	}
	if _, err := os.Stat(binary); os.IsNotExist(err) {
		result.noTests = true
		return result, nil
	}

	profile := filepath.Join(folder, "cover.out")
	test := process.Command{
		Program:   binary,
		Arguments: []string{"-test.count=1", "-test.timeout=10m", "-test.coverprofile=" + profile},
		Folder:    pkg.Dir,
		Env:       c.settings.testEnv(),
	}
	started = time.Now()
	tested, err := test.Run(ctx)
	result.baseline.test = time.Since(started)
	if err != nil {
		return result, err
	}
	if tested.Code != 0 {
		return result, fmt.Errorf("the tests of %s fail with the real code, so no mutant can get a verdict:\n%s", pkg.ImportPath, strings.TrimSpace(tested.Tail))
	}
	result.blocks, err = c.readProfile(folderOf, profile)
	return result, err
}

func (c *coverage) readProfile(folderOf map[string]string, profile string) (map[string][]block, error) {
	file, err := os.Open(profile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	blocks := map[string][]block{}
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		line := lines.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		name, rest, found := strings.Cut(line, ":")
		var b block
		var statements int
		if !found {
			return nil, fmt.Errorf("read the coverage line %q", line)
		}
		if _, err := fmt.Sscanf(rest, "%d.%d,%d.%d %d %d", &b.startLine, &b.startColumn, &b.endLine, &b.endColumn, &statements, &b.count); err != nil {
			return nil, fmt.Errorf("read the coverage line %q: %w", line, err)
		}
		dir, found := folderOf[path.Dir(name)]
		if !found {
			continue
		}
		relative, err := filepath.Rel(c.root, filepath.Join(dir, path.Base(name)))
		if err != nil {
			return nil, err
		}
		key := filepath.ToSlash(relative)
		blocks[key] = append(blocks[key], b)
	}
	return blocks, lines.Err()
}
