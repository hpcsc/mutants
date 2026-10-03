package golang

import (
	"bufio"
	"context"
	"go/build/constraint"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var goVersion = regexp.MustCompile(`^go1\.\d+$`)

func TagsOfTests(ctx context.Context, root string) (map[string]int, error) {
	builtIn, err := builtInTags(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		tags, err := neededTags(path)
		if err != nil {
			return err
		}
		for tag := range tags {
			if !builtIn[tag] && !goVersion.MatchString(tag) {
				counts[tag]++
			}
		}
		return nil
	})
	return counts, err
}

// builtInTags asks go for each operating system and architecture, so a new one needs no change here.
func builtInTags(ctx context.Context) (map[string]bool, error) {
	output, err := exec.CommandContext(ctx, "go", "tool", "dist", "list").Output()
	if err != nil {
		return nil, err
	}
	tags := map[string]bool{"cgo": true, "gc": true, "gccgo": true, "ignore": true, "unix": true}
	for _, platform := range strings.Fields(string(output)) {
		system, architecture, _ := strings.Cut(platform, "/")
		tags[system], tags[architecture] = true, true
	}
	return tags, nil
}

func neededTags(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	tags := map[string]bool{}
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if strings.HasPrefix(line, "package ") {
			break
		}
		if !constraint.IsGoBuild(line) {
			continue
		}
		expression, err := constraint.Parse(line)
		if err != nil {
			continue
		}
		collect(expression, false, tags)
	}
	return tags, lines.Err()
}

func collect(expression constraint.Expr, negated bool, tags map[string]bool) {
	switch expression := expression.(type) {
	case *constraint.TagExpr:
		if !negated {
			tags[expression.Tag] = true
		}
	case *constraint.NotExpr:
		collect(expression.X, !negated, tags)
	case *constraint.AndExpr:
		collect(expression.X, negated, tags)
		collect(expression.Y, negated, tags)
	case *constraint.OrExpr:
		collect(expression.X, negated, tags)
		collect(expression.Y, negated, tags)
	}
}
