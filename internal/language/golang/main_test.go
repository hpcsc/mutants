//go:build unit

package golang_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hpcsc/mutants/internal/language/golang"
)

// buildCacheLog makes this test binary the cache program of go, so that a test can build through it and
// read from the log which folder took the new entries.
const buildCacheLog = "MUTANTS_TEST_BUILD_CACHE_LOG"

func TestMain(m *testing.M) {
	if log := os.Getenv(buildCacheLog); log != "" && len(os.Args) == 3 {
		os.Exit(serveBuildCache(log, os.Args[1], os.Args[2]))
	}
	os.Exit(m.Run())
}

func serveBuildCache(log, userCache, mutantCache string) int {
	file, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(file, mutantCache)
	file.Close()
	if err := golang.ServeBuildCache(os.Stdin, os.Stdout, userCache, mutantCache); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
