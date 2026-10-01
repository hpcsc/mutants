package golang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
)

type goPackage struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	CgoFiles   []string
}

func (p goPackage) builds(base string) bool {
	return slices.Contains(p.GoFiles, base) || slices.Contains(p.CgoFiles, base)
}

type packageFinder struct {
	tags     []string
	mutex    sync.Mutex
	packages map[string]foundPackage
}

type foundPackage struct {
	found goPackage
	err   error
}

func newPackageFinder(tags []string) *packageFinder {
	return &packageFinder{tags: tags, packages: map[string]foundPackage{}}
}

func (f *packageFinder) find(ctx context.Context, folder string) (goPackage, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if result, found := f.packages[folder]; found {
		return result.found, result.err
	}
	result := foundPackage{}
	arguments := []string{"list", "-find", "-json"}
	if len(f.tags) > 0 {
		arguments = append(arguments, "-tags="+strings.Join(f.tags, ","))
	}
	command := exec.CommandContext(ctx, "go", append(arguments, ".")...)
	command.Dir = folder
	output, err := command.Output()
	var exitError *exec.ExitError
	switch {
	case errors.As(err, &exitError):
		result.err = fmt.Errorf("go list in %s: %s", folder, strings.TrimSpace(string(exitError.Stderr)))
	case err != nil:
		result.err = fmt.Errorf("go list in %s: %w", folder, err)
	default:
		result.err = json.Unmarshal(output, &result.found)
	}
	if ctx.Err() == nil {
		f.packages[folder] = result
	}
	return result.found, result.err
}
