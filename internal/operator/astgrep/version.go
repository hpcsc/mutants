package astgrep

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const MinimumVersion = "0.45.0"

func CheckVersion(ctx context.Context) error {
	output, err := exec.CommandContext(ctx, "ast-grep", "--version").Output()
	if err != nil {
		return fmt.Errorf("mutants needs ast-grep %s or later on the PATH: %w", MinimumVersion, err)
	}
	installed := strings.TrimPrefix(strings.TrimSpace(string(output)), "ast-grep ")
	if versionBefore(installed, MinimumVersion) {
		return fmt.Errorf("mutants needs ast-grep %s or later, and the PATH has %s", MinimumVersion, installed)
	}
	return nil
}

func versionBefore(installed, minimum string) bool {
	installedParts, minimumParts := strings.Split(installed, "."), strings.Split(minimum, ".")
	for i, part := range minimumParts {
		if i >= len(installedParts) {
			return true
		}
		have, err := strconv.Atoi(strings.TrimFunc(installedParts[i], func(r rune) bool { return r < '0' || r > '9' }))
		if err != nil {
			return true
		}
		want, _ := strconv.Atoi(part)
		if have != want {
			return have < want
		}
	}
	return false
}
