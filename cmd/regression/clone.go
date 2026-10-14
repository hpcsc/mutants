package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const venv = ".venv"

type clone struct {
	repository
	folder string
	work   string
}

func prepare(ctx context.Context, r repository, work string, log io.Writer) (*clone, error) {
	c := &clone{repository: r, folder: filepath.Join(work, r.Name), work: work}
	if _, err := os.Stat(filepath.Join(c.folder, ".git")); err != nil {
		fmt.Fprintf(log, "%s: clone %s\n", r.Name, r.URL)
		if err := os.MkdirAll(work, 0o755); err != nil {
			return nil, err
		}
		if _, err := git(ctx, work, "clone", "--quiet", "--filter=blob:none", "--no-checkout", r.URL, c.folder); err != nil {
			return nil, err
		}
	}
	if _, err := c.git(ctx, "cat-file", "-e", r.Commit+"^{commit}"); err != nil {
		if _, err := c.git(ctx, "fetch", "--quiet", "origin", r.Commit); err != nil {
			return nil, err
		}
	}
	if err := c.excludeLocalFolders(); err != nil {
		return nil, err
	}
	if err := c.checkout(ctx, r.Commit); err != nil {
		return nil, err
	}
	if r.Language == "python" {
		if err := c.install(ctx, log); err != nil {
			return nil, fmt.Errorf("%s: install the venv: %w", r.Name, err)
		}
	}
	return c, nil
}

// Hex goes to the work folder, and not to the Mix home of the user.
func (c *clone) env() []string {
	if c.Language != "elixir" {
		return nil
	}
	return append(os.Environ(), "MIX_HOME="+filepath.Join(c.work, "mix"), "HEX_HOME="+filepath.Join(c.work, "hex"))
}

func (c *clone) git(ctx context.Context, arguments ...string) (string, error) {
	return git(ctx, c.folder, arguments...)
}

func (c *clone) sourceCommits(ctx context.Context, count int) ([]string, error) {
	arguments := append([]string{"log", "--no-merges", "--format=%H %P", "-n", fmt.Sprint(count), c.Commit, "--"}, c.sourcePathspecs()...)
	output, err := c.git(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	var commits []string
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			commits = append(commits, fields[0])
		}
	}
	return commits, nil
}

func (c *clone) checkout(ctx context.Context, commit string) error {
	if _, err := c.git(ctx, "checkout", "--quiet", "--force", "--detach", commit); err != nil {
		return err
	}
	arguments := []string{"clean", "-ffdxq"}
	for _, folder := range c.localFolders() {
		arguments = append(arguments, "-e", folder)
	}
	if _, err := c.git(ctx, arguments...); err != nil {
		return err
	}
	if c.Language == "elixir" {
		if err := c.getDependencies(ctx); err != nil {
			return fmt.Errorf("%s: get the dependencies: %w", c.Name, err)
		}
	}
	return nil
}

func (c *clone) status(ctx context.Context) (string, error) {
	arguments := []string{"status", "--porcelain", "--ignored", "--untracked-files=all", "--", "."}
	for _, folder := range c.localFolders() {
		arguments = append(arguments, ":(exclude)"+folder)
	}
	return c.git(ctx, arguments...)
}

// mutants tests each line of an untracked file, so a local folder must not be untracked
func (c *clone) excludeLocalFolders() error {
	path := filepath.Join(c.folder, ".git", "info", "exclude")
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	excluded := strings.Fields(string(content))
	for _, folder := range c.localFolders() {
		if !slices.Contains(excluded, folder) {
			content = append(content, []byte("\n"+folder+"\n")...)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func (c *clone) python() string {
	return filepath.Join(c.folder, venv, "bin", "python")
}

func (c *clone) install(ctx context.Context, log io.Writer) error {
	installed := filepath.Join(c.folder, venv, "regression-install")
	want := strings.Join(c.Install, "\n")
	if content, err := os.ReadFile(installed); err == nil && string(content) == want {
		return nil
	}
	fmt.Fprintf(log, "%s: pip install %s\n", c.Name, strings.Join(c.Install, " "))
	if err := os.RemoveAll(filepath.Join(c.folder, venv)); err != nil {
		return err
	}
	steps := []command{
		{program: "python3", arguments: []string{"-m", "venv", venv}},
		{program: c.python(), arguments: append([]string{"-m", "pip", "install", "--quiet", "--disable-pip-version-check"}, c.Install...)},
	}
	for _, step := range steps {
		step.folder, step.limit = c.folder, 15*time.Minute
		result, err := step.run(ctx)
		if err != nil {
			return err
		}
		if result.code != 0 {
			return fmt.Errorf("%s: %s", step.program, strings.TrimSpace(result.output()))
		}
	}
	return os.WriteFile(installed, []byte(want), 0o644)
}

// getDependencies installs Hex and rebar3 only for a project with a mix.lock, so that a project with no
// dependency needs no network.
func (c *clone) getDependencies(ctx context.Context) error {
	var steps []command
	if _, err := os.Stat(filepath.Join(c.folder, "mix.lock")); err == nil {
		steps = append(steps,
			command{program: "mix", arguments: []string{"local.hex", "--force", "--if-missing"}},
			command{program: "mix", arguments: []string{"local.rebar", "--force", "--if-missing"}},
		)
	}
	steps = append(steps, command{program: "mix", arguments: []string{"deps.get", "--only", "test"}})
	for _, step := range steps {
		step.folder, step.limit, step.env = c.folder, 15*time.Minute, append(c.env(), "MIX_ENV=test")
		result, err := step.run(ctx)
		if err != nil {
			return err
		}
		if result.code != 0 {
			return fmt.Errorf("mix %s: %s", strings.Join(step.arguments, " "), strings.TrimSpace(result.output()))
		}
	}
	return nil
}
