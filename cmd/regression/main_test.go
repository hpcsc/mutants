//go:build unit

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const discount = `package calc

func Discount(total int) int {
	if total >= 100 {
		return 10
	}
	return 0
}
`

const discountTest = `package calc

import "testing"

func TestDiscount(t *testing.T) {
	if Discount(150) != 10 {
		t.Fatal("want 10")
	}
	if Discount(50) != 0 {
		t.Fatal("want 0")
	}
}
`

const fakeMutants = `#!/bin/sh
if [ "$1" = rerun ]; then
	line=$(awk -F'|' -v id="$4" '$1 == id' "$FAKE_RERUNS")
	printf '%s: %s\n' "$(echo "$line" | cut -d'|' -f2)" "$4"
	exit "$(echo "$line" | cut -d'|' -f3)"
fi
if [ -n "$FAKE_SECOND_REPORT" ] && [ -e "$FAKE_STATE" ]; then
	cat "$FAKE_SECOND_REPORT"
else
	touch "$FAKE_STATE"
	cat "$FAKE_REPORT"
fi
if [ -n "$FAKE_WRITE" ]; then
	touch "$FAKE_WRITE"
fi
if [ -n "$FAKE_SLEEP_PID" ]; then
	sleep 60 >/dev/null 2>&1 &
	echo $! > "$FAKE_SLEEP_PID"
fi
printf '%s' "$FAKE_STDERR" >&2
exit "$FAKE_EXIT"
`

const flakyTest = `package calc

import (
	"os"
	"strconv"
	"testing"
)

func TestDiscountAtTheBoundary(t *testing.T) {
	content, _ := os.ReadFile(os.Getenv("FLAKY_COUNTER"))
	count, _ := strconv.Atoi(string(content))
	count++
	if err := os.WriteFile(os.Getenv("FLAKY_COUNTER"), []byte(strconv.Itoa(count)), 0o644); err != nil {
		t.Fatal(err)
	}
	if count%2 == 0 && Discount(100) != 10 {
		t.Fatal("want 10 at 100")
	}
}
`

const flakyRealCodeTest = `package calc

import (
	"os"
	"strconv"
	"testing"
)

func TestEachSecondRun(t *testing.T) {
	content, _ := os.ReadFile(os.Getenv("FLAKY_COUNTER"))
	count, _ := strconv.Atoi(string(content))
	count++
	if err := os.WriteFile(os.Getenv("FLAKY_COUNTER"), []byte(strconv.Itoa(count)), 0o644); err != nil {
		t.Fatal(err)
	}
	if count%2 == 0 {
		t.Fatal("fails in each second run")
	}
}
`

var (
	boundary        = reportedMutant{ID: "calc/calc.go:Discount:CONDITIONALS_BOUNDARY#1", File: "calc/calc.go", Line: 4, Column: 5, Operator: "CONDITIONALS_BOUNDARY", Original: "total >= 100", Replacement: "total > 100"}
	smallerDiscount = reportedMutant{ID: "calc/calc.go:Discount:INTEGER_DECREMENT#1", File: "calc/calc.go", Line: 5, Column: 10, Operator: "INTEGER_DECREMENT", Original: "10", Replacement: "9"}
)

func TestRegression(t *testing.T) {
	t.Run("plain tests", func(t *testing.T) {
		t.Run("a LIVED mutant that a plain test kills is a finding", func(t *testing.T) {
			fake := newFake(t, withStatus(smallerDiscount, lived))

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, "FINDING plain tests "+smallerDiscount.ID+": LIVED, but a plain test fails: --- FAIL: TestDiscount")
		})

		t.Run("a KILLED mutant that each plain test lets live is a finding", func(t *testing.T) {
			fake := newFake(t, withStatus(boundary, killed))

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, "FINDING plain tests "+boundary.ID+": KILLED, but each plain test passes")
		})

		t.Run("mutants whose verdicts the plain tests repeat give no finding", func(t *testing.T) {
			fake := newFake(t, withStatus(boundary, lived), withStatus(smallerDiscount, killed))

			output, code := fake.check(t)

			require.Equal(t, 0, code, output)
			require.Contains(t, output, "2 mutants (KILLED 1, LIVED 1), 2 reruns, 2 plain tests")
		})

		t.Run("a LIVED mutant that a plain test kills in some runs only is a skip", func(t *testing.T) {
			fake := newFakeWith(t, map[string]string{"calc/flaky_test.go": flakyTest}, withStatus(boundary, lived))
			fake.env["FLAKY_COUNTER"] = filepath.Join(t.TempDir(), "counter")

			output, code := fake.check(t)

			require.Equal(t, 0, code, output)
			require.Contains(t, output, "skip: "+boundary.ID+": LIVED, but a plain test fails: --- FAIL: TestDiscountAtTheBoundary")
			require.Contains(t, output, "and the plain tests pass in 1 of 2 runs with the mutant")
		})

		t.Run("a mutant that the report places on other code is a finding", func(t *testing.T) {
			misplaced := boundary
			misplaced.Column = 6
			fake := newFake(t, withStatus(misplaced, lived))

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, `FINDING plain tests `+boundary.ID+`: the report gives a wrong place: calc/calc.go:4:6 does not hold "total >= 100"`)
		})

		t.Run("a stop for tests that fail with the real code is a skip when the plain tests pass in some runs only", func(t *testing.T) {
			fake := newFakeWith(t, map[string]string{"calc/flaky_test.go": flakyRealCodeTest})
			fake.env["FLAKY_COUNTER"] = filepath.Join(t.TempDir(), "counter")
			fake.env["FAKE_EXIT"] = "2"
			fake.env["FAKE_STDERR"] = "the tests of example.com/shop/calc fail with the real code, so no mutant can get a verdict:\n--- FAIL: TestEachSecondRun"

			output, code := fake.check(t)

			require.Equal(t, 0, code, output)
			require.Contains(t, output, "skip: mutants says that the tests fail with the real code, but a plain run of them passes")
			require.Contains(t, output, "and the plain tests pass in 1 of 2 runs with the real code")
		})

		t.Run("a stop for tests that fail with the real code is a finding when the plain tests pass", func(t *testing.T) {
			fake := newFake(t)
			fake.env["FAKE_EXIT"] = "2"
			fake.env["FAKE_STDERR"] = "the tests of example.com/shop/calc fail with the real code, so no mutant can get a verdict:\n--- FAIL: TestDiscount"

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, "FINDING plain tests : mutants says that the tests fail with the real code, but a plain run of them passes")
		})
	})

	t.Run("run", func(t *testing.T) {
		t.Run("an exit code that mutants does not give is a finding", func(t *testing.T) {
			fake := newFake(t, withStatus(boundary, lived))
			fake.env["FAKE_EXIT"] = "3"

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, "FINDING exit code : mutants run exited with code 3")
		})

		t.Run("a file that the run leaves in the work tree is a finding", func(t *testing.T) {
			fake := newFake(t, withStatus(boundary, lived))
			fake.env["FAKE_WRITE"] = "left.txt"

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, "FINDING work tree")
			require.Contains(t, output, "?? left.txt")
		})

		t.Run("a process that the run leaves alive is a finding, and the regression test stops it", func(t *testing.T) {
			fake := newFake(t, withStatus(boundary, lived))
			pidFile := filepath.Join(t.TempDir(), "pid")
			fake.env["FAKE_SLEEP_PID"] = pidFile

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, "FINDING processes : alive after the run: ")
			require.Contains(t, output, "sleep 60")
			content, err := os.ReadFile(pidFile)
			require.NoError(t, err)
			pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
			require.NoError(t, err)
			require.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil }, 5*time.Second, 100*time.Millisecond)
		})

		t.Run("a verdict that changes in the second run is a skip when the plain tests pass and fail with the mutant", func(t *testing.T) {
			fake := newFakeWith(t, map[string]string{"calc/flaky_test.go": flakyTest}, withStatus(boundary, lived))
			fake.env["FAKE_SECOND_REPORT"] = fake.writeReport(t, "second", withStatus(boundary, killed))
			fake.env["FLAKY_COUNTER"] = filepath.Join(t.TempDir(), "counter")

			output, code := fake.check(t, "-repeat")

			require.Equal(t, 0, code, output)
			require.Contains(t, output, `skip: `+boundary.ID+`: LIVED in the first run, "KILLED" in the second, and the plain tests pass in 1 of 2 runs with the mutant`)
		})

		t.Run("a verdict that changes in the second run is a finding", func(t *testing.T) {
			fake := newFake(t, withStatus(boundary, lived))
			fake.env["FAKE_SECOND_REPORT"] = fake.writeReport(t, "second", withStatus(boundary, killed))

			output, code := fake.check(t, "-repeat")

			require.Equal(t, 1, code)
			require.Contains(t, output, `FINDING second run `+boundary.ID+`: LIVED in the first run, "KILLED" in the second`)
		})
	})

	t.Run("rerun", func(t *testing.T) {
		t.Run("a rerun with another status than the run is a finding", func(t *testing.T) {
			fake := newFake(t, withStatus(boundary, lived))
			fake.writeReruns(t, boundary.ID+"|KILLED|0")

			output, code := fake.check(t)

			require.Equal(t, 1, code)
			require.Contains(t, output, "FINDING rerun "+boundary.ID+`: LIVED in the run, but rerun exits 0 with "KILLED"`)
		})
	})
}

type fake struct {
	folder       string
	repositories string
	env          map[string]string
}

func withStatus(m reportedMutant, status string) reportedMutant {
	m.Status = status
	return m
}

func newFake(t *testing.T, mutants ...reportedMutant) *fake {
	t.Helper()
	return newFakeWith(t, nil, mutants...)
}

func newFakeWith(t *testing.T, files map[string]string, mutants ...reportedMutant) *fake {
	t.Helper()
	f := &fake{folder: t.TempDir(), env: map[string]string{"FAKE_EXIT": "10"}}
	repository := filepath.Join(f.folder, "shop")
	files = maps.Clone(files)
	if files == nil {
		files = map[string]string{}
	}
	files["calc/calc.go"], files["calc/calc_test.go"] = discount, discountTest
	commit := gitRepository(t, repository, files)
	f.repositories = filepath.Join(f.folder, "repositories.yml")
	list := "- name: shop\n  url: " + repository + "\n  commit: " + commit + "\n  language: go\n"
	require.NoError(t, os.WriteFile(f.repositories, []byte(list), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.folder, "mutants"), []byte(fakeMutants), 0o755))

	f.env["FAKE_STATE"] = filepath.Join(f.folder, "state")
	f.env["FAKE_REPORT"] = f.writeReport(t, "first", mutants...)
	var reruns []string
	for _, m := range mutants {
		reruns = append(reruns, m.ID+"|"+m.Status+"|"+strconv.Itoa(rerunExit(m.Status)))
	}
	f.writeReruns(t, reruns...)
	return f
}

func (f *fake) writeReport(t *testing.T, name string, mutants ...reportedMutant) string {
	t.Helper()
	content, err := json.Marshal(report{Mutants: append([]reportedMutant{}, mutants...)})
	require.NoError(t, err)
	path := filepath.Join(f.folder, name+".json")
	require.NoError(t, os.WriteFile(path, content, 0o644))
	return path
}

func (f *fake) writeReruns(t *testing.T, lines ...string) {
	t.Helper()
	path := filepath.Join(f.folder, "reruns")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	f.env["FAKE_RERUNS"] = path
}

func (f *fake) check(t *testing.T, flags ...string) (string, int) {
	t.Helper()
	for name, value := range f.env {
		t.Setenv(name, value)
	}
	arguments := append([]string{
		"-repositories", f.repositories,
		"-work", filepath.Join(f.folder, "work"),
		"-mutants", filepath.Join(f.folder, "mutants"),
		"-commits", "1",
		"-sample", "1",
		"-plain-runs", "3",
		"-repeat=false",
	}, flags...)
	var output bytes.Buffer
	code := run(context.Background(), arguments, &output, &output)
	return output.String(), code
}

func gitRepository(t *testing.T, folder string, files map[string]string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(folder, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "go.mod"), []byte("module example.com/shop\n\ngo 1.22\n"), 0o644))
	git := func(arguments ...string) string {
		command := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, arguments...)...)
		command.Dir = folder
		output, err := command.CombinedOutput()
		require.NoError(t, err, string(output))
		return strings.TrimSpace(string(output))
	}
	git("init", "--quiet")
	git("add", "go.mod")
	git("commit", "--quiet", "-m", "start the module")
	for path, content := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(folder, path)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(folder, path), []byte(content), 0o644))
	}
	git("add", ".")
	git("commit", "--quiet", "-m", "add the discount")
	return git("rev-parse", "HEAD")
}
