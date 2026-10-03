//go:build unit

package python_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	pytestVersion   = "9.1.1"
	coverageVersion = "7.16.2"
)

var withCoverage, withoutCoverage string

func TestMain(m *testing.M) {
	var err error
	withCoverage, err = venv("pytest=="+pytestVersion, "coverage=="+coverageVersion)
	if err == nil {
		withoutCoverage, err = venv("pytest==" + pytestVersion)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// venv makes a venv with the packages one time in the cache of the user, so that a later run of the tests
// needs no network.
func venv(packages ...string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	name := "test-python"
	for _, p := range packages {
		name += "-" + p
	}
	folder := filepath.Join(cache, "mutants", name)
	python := filepath.Join(folder, "bin", "python")
	if _, err := os.Stat(python); err == nil {
		return python, nil
	}
	temporary := folder + fmt.Sprintf(".%d", os.Getpid())
	if output, err := exec.Command("uv", "venv", "--quiet", temporary).CombinedOutput(); err != nil {
		return "", fmt.Errorf("make the venv %s with uv: %w\n%s", temporary, err, output)
	}
	install := append([]string{"pip", "install", "--quiet", "--python", filepath.Join(temporary, "bin", "python")}, packages...)
	if output, err := exec.Command("uv", install...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("install %v with uv: %w\n%s", packages, err, output)
	}
	if err := os.Rename(temporary, folder); err != nil {
		os.RemoveAll(temporary)
		if _, statErr := os.Stat(python); statErr != nil {
			return "", err
		}
	}
	return python, nil
}
