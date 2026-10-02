import { existsSync, readdirSync, readFileSync, utimesSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { git, goRepository, type ReportedMutant, runCli, runMutants, scratchDir, startCli, verdicts, writeFiles } from '../testUtils'

const figures = `package figures

type Totals struct {
	Paid, Owed int
}

func Summarise(paid, owed int) Totals {
	return Totals{Paid: paid, Owed: owed}
}
`

function figuresTest(paid: number, owed: number): string {
  return `package figures

import "testing"

func TestSummarise(t *testing.T) {
	if got := Summarise(${paid}, ${owed}); got != (Totals{Paid: ${paid}, Owed: ${owed}}) {
		t.Fatalf("got %v", got)
	}
}
`
}

const accounts = `package accounts

import "fmt"

func Load(id int) (int, error) {
	if id < 0 {
		return 0, fmt.Errorf("load the account %d: the id is negative", id)
	}
	return id, nil
}
`

const accountsTest = `package accounts

import "testing"

func TestLoad(t *testing.T) {
	if id, err := Load(1); err != nil || id != 1 {
		t.Fatalf("got %d, %v", id, err)
	}
}
`

const total = `package total

func Sum(items []int) int {
	sum := 0
	for _, item := range items {
		sum += item
	}
	return sum
}
`

function totalTest(items: number[]): string {
  const sum = items.reduce((a, b) => a + b, 0)
  return `package total

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]int{${items.join(', ')}}); got != ${sum} {
		t.Fatalf("got %d", got)
	}
}
`
}

const allow = `package allow

import "slices"

func Allowed(items []string, id string) bool {
	requested := items
	if id == "" || !slices.Contains(requested, id) {
		return false
	}
	return true
}
`

const allowTest = `package allow

import "testing"

func TestAllowed(t *testing.T) {
	if !Allowed([]string{"a"}, "a") || Allowed(nil, "") {
		t.Fatal("Allowed gives the wrong answer")
	}
}
`

const wait = `package wait

import "time"

func Expired(now, deadline time.Time) bool {
	return now.After(deadline)
}
`

function waitTest(offsets: string[]): string {
  const checks = offsets.map((offset) => `	if got := Expired(deadline.Add(${offset}), deadline); got != (${offset} > 0) {
		t.Errorf("Expired at %v: got %v", ${offset}, got)
	}`)
  return `package wait

import (
	"testing"
	"time"
)

func TestExpired(t *testing.T) {
	deadline := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
${checks.join('\n')}
}
`
}

const due = `package due

import "time"

func Due(start time.Time, days int) time.Time {
	return start.AddDate(0, 0, days)
}
`

const calendarDay = `id: CALENDAR_DAY
language: go
rule:
  pattern:
    context: 'func f() { _ = $T.AddDate(0, 0, $N) }'
    selector: call_expression
fix: $T.Add(time.Duration($N) * 24 * time.Hour)
`

function dueTest(location: string): string {
  return `package due

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestDue(t *testing.T) {
	location, err := time.LoadLocation("${location}")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 2, 9, 0, 0, 0, location)
	if got, want := Due(start, 3), time.Date(2026, 10, 5, 9, 0, 0, 0, location); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
`
}

const gate = `package gate

type Store interface {
	Has(id string) bool
}

type Checker struct {
	store Store
}

func NewChecker(store Store) *Checker {
	return &Checker{store: store}
}

func (c *Checker) Allow(id string) bool {
	if id == "" {
		return false
	}
	return c.closing(id)
}

func (c *Checker) closing(id string) bool {
	if c.store == nil {
		return true
	}
	return c.store.Has(id)
}
`

const gateTest = `package gate

import "testing"

type store map[string]bool

func (s store) Has(id string) bool { return s[id] }

func TestAllow(t *testing.T) {
	if NewChecker(store{"a": true}).Allow("") || !NewChecker(store{"a": true}).Allow("a") || !NewChecker(nil).Allow("b") {
		t.Fatal("Allow")
	}
}
`

const handler = `package handler

import "example.com/fixture/gate"

type Allower interface {
	Allow(id string) bool
}

type Handler struct {
	allow Allower
}

func New(allow Allower) *Handler {
	return &Handler{allow: allow}
}

func Main() *Handler {
	return New(gate.NewChecker(nil))
}

func (h *Handler) Handle(id string) string {
	if h.allow.Allow(id) {
		return "ok"
	}
	return "no"
}
`

const handlerTest = `package handler

import "testing"

type always bool

func (a always) Allow(string) bool { return bool(a) }

func TestHandle(t *testing.T) {
	if New(always(true)).Handle("a") != "ok" {
		t.Fatal("Handle")
	}
}
`

const maxSource = `package calc

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
`

const maxTest = `package calc

import "testing"

func TestMax(t *testing.T) {
	if Max(1, 2) != 2 || Max(2, 1) != 2 {
		t.Fatal("Max gives the wrong number")
	}
}
`

const maxWithComments = `// Package calc compares numbers.
// It has no state.
${maxSource}`

describe('mutants run', { timeout: 240_000 }, () => {
  it('a swap of two fields lives when the test uses two equal values, and dies when they differ', async () => {
    const equal = goRepository()
    writeFiles(equal, { 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 5) })
    const different = goRepository()
    writeFiles(different, { 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 7) })

    const lives = await runMutants(equal, ['--base', 'HEAD', '--operators', 'NAMED_VALUE_SWAP'])
    const dies = await runMutants(different, ['--base', 'HEAD', '--operators', 'NAMED_VALUE_SWAP'])

    expect(verdicts(lives.mutants)).toEqual(['NAMED_VALUE_SWAP paid, Owed: owed LIVED'])
    expect(lives.result.status).toBe(10)
    expect(verdicts(dies.mutants)).toEqual(['NAMED_VALUE_SWAP paid, Owed: owed KILLED'])
    expect(dies.result.status).toBe(0)
  })

  it('in an error branch that no test enters, BRANCH_IF lives and the return in it is not covered', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'accounts/accounts.go': accounts, 'accounts/accounts_test.go': accountsTest })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'BRANCH_IF,ERROR_REMOVE'])

    expect(mutants.map((m) => `${m.operator} ${m.line} ${m.status}`)).toEqual(['BRANCH_IF 6 LIVED', 'ERROR_REMOVE 7 NOT COVERED'])
  })

  it('stopping a loop after its first item lives when the test has one item, and dies when it has two', async () => {
    const one = goRepository()
    writeFiles(one, { 'total/total.go': total, 'total/total_test.go': totalTest([3]) })
    const two = goRepository()
    writeFiles(two, { 'total/total.go': total, 'total/total_test.go': totalTest([3, 4]) })

    const lives = await runMutants(one, ['--base', 'HEAD', '--operators', 'BREAK_AT_END'])
    const dies = await runMutants(two, ['--base', 'HEAD', '--operators', 'BREAK_AT_END'])

    expect(lives.mutants.map((m) => m.status)).toEqual(['LIVED'])
    expect(dies.mutants.map((m) => m.status)).toEqual(['KILLED'])
  })

  it('the boundary of a time comparison lives when no test uses the time itself, and dies when one does', async () => {
    const around = goRepository()
    writeFiles(around, { 'wait/wait.go': wait, 'wait/wait_test.go': waitTest(['-time.Hour', 'time.Hour']) })
    const at = goRepository()
    writeFiles(at, { 'wait/wait.go': wait, 'wait/wait_test.go': waitTest(['-time.Hour', '0', 'time.Hour']) })

    const lives = await runMutants(around, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])
    const dies = await runMutants(at, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    expect(verdicts(lives.mutants)).toEqual(['CONDITIONALS_BOUNDARY now.After(deadline) LIVED'])
    expect(verdicts(dies.mutants)).toEqual(['CONDITIONALS_BOUNDARY now.After(deadline) KILLED'])
  })

  it('the mutant of a CALENDAR_DAY rule in the repository lives when the test has no change of daylight saving time, and dies when it has one', async () => {
    const utc = goRepository()
    writeFiles(utc, {
      '.mutants/operators/go/CALENDAR_DAY.yml': calendarDay,
      'due/due.go': due,
      'due/due_test.go': dueTest('UTC'),
    })
    const sydney = goRepository()
    writeFiles(sydney, {
      '.mutants/operators/go/CALENDAR_DAY.yml': calendarDay,
      'due/due.go': due,
      'due/due_test.go': dueTest('Australia/Sydney'),
    })

    const lives = await runMutants(utc, ['--base', 'HEAD', '--operators', 'CALENDAR_DAY'])
    const dies = await runMutants(sydney, ['--base', 'HEAD', '--operators', 'CALENDAR_DAY'])

    expect(verdicts(lives.mutants)).toEqual(['CALENDAR_DAY start.AddDate(0, 0, days) LIVED'])
    expect(verdicts(dies.mutants)).toEqual(['CALENDAR_DAY start.AddDate(0, 0, days) KILLED'])
  })

  it('a mutant that removes the only use of a variable and of an import builds, and lives', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'allow/allow.go': allow, 'allow/allow_test.go': allowTest })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'EXPRESSION_REMOVE'])

    expect(mutants.map((m) => `${m.replacement} ${m.status}`)).toEqual([
      'false || !slices.Contains(requested, id) LIVED',
      'id == "" || false LIVED',
    ])
  })

  it('a busy loop and a removed close time out, and no process that a test of a mutant starts stays alive', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'count/count.go': `package count

func Count(n int) int {
	c := 0
	for i := 0; i < n; i++ {
		c = c + 1
	}
	return c
}
`,
      'count/count_test.go': `package count

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestCount(t *testing.T) {
	child := exec.Command("sleep", "120")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	children, err := os.OpenFile("children.pid", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(children, child.Process.Pid)
	children.Close()
	if Count(3) != 3 {
		t.Fatal("Count(3) is not 3")
	}
}
`,
      'count/wait.go': `package count

func Wait(work func()) {
	done := make(chan struct{})
	go func() {
		work()
		close(done)
	}()
	<-done
}
`,
      'count/wait_test.go': `package count

import (
	"testing"
	"time"
)

func TestWait(t *testing.T) {
	// a pending timer stops the runtime from reporting a deadlock, so a missing close makes the test hang
	time.AfterFunc(time.Hour, func() {})
	called := false
	Wait(func() { called = true })
	if !called {
		t.Fatal("Wait did not call the work")
	}
}
`,
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'INCREMENT_DECREMENT,STATEMENT_REMOVE'])

    expect(verdicts(mutants)).toEqual([
      'INCREMENT_DECREMENT i++ TIMED OUT',
      'STATEMENT_REMOVE c = c + 1 KILLED',
      'STATEMENT_REMOVE work() KILLED',
      'STATEMENT_REMOVE close(done) TIMED OUT',
    ])
    const children = readFileSync(join(dir, 'count', 'children.pid'), 'utf8').trim().split('\n').map(Number)
    expect(children.length).toBeGreaterThan(2)
    await vi.waitFor(
      () => {
        for (const child of children) {
          expect(() => process.kill(child, 0)).toThrow()
        }
      },
      { timeout: 5_000, interval: 100 },
    )
  })

  it('stops at SIGTERM with exit 130, and stops the tests of the mutant that runs', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'wait/wait.go': `package wait

func Wait() {
	done := make(chan struct{})
	go func() {
		close(done)
	}()
	<-done
}
`,
      'wait/wait_test.go': `package wait

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWait(t *testing.T) {
	child := exec.Command("sleep", "120")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	children, err := os.OpenFile("children.pid", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(children, child.Process.Pid)
	children.Close()
	// a pending timer stops the runtime from reporting a deadlock, so a missing close makes the test hang
	time.AfterFunc(time.Hour, func() {})
	Wait()
}
`,
    })
    const pids = join(dir, 'wait', 'children.pid')
    const children = () => (existsSync(pids) ? readFileSync(pids, 'utf8').trim().split('\n').map(Number) : [])

    const { child, result } = startCli(dir, ['run', '--base', 'HEAD', '--operators', 'STATEMENT_REMOVE'])
    // the first pid comes from the coverage run, and the second from the test of the mutant
    await vi.waitFor(() => expect(children()).toHaveLength(2), { timeout: 120_000, interval: 100 })
    child.kill('SIGTERM')
    const { status, stderr } = await result

    expect(status).toBe(130)
    expect(stderr).toContain('the run was interrupted')
    await vi.waitFor(
      () => {
        for (const pid of children()) {
          expect(() => process.kill(pid, 0)).toThrow()
        }
      },
      { timeout: 5_000, interval: 100 },
    )
  })

  it('with --all, runs each line of the files in the folder, also committed lines, and no file of another folder', async () => {
    const dir = goRepository({
      'calc/calc.go': maxSource,
      'calc/calc_test.go': maxTest,
      'other/max.go': maxSource.replace('package calc', 'package other'),
    })

    const { result, mutants } = await runMutants(dir, ['--all', 'calc', '--operators', 'CONDITIONALS_NEGATION'])

    expect(mutants.map((m) => `${m.id} ${m.status}`)).toEqual(['calc/calc.go:Max:CONDITIONALS_NEGATION#1 KILLED'])
    expect(result.status).toBe(0)
  })

  it('with no flags in a clone, runs the mutants of the commits that the branch adds to the default branch of origin', async () => {
    const clone = scratchDir()
    git(clone, 'clone', '--quiet', goRepository({ 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest }), '.')
    git(clone, 'switch', '--quiet', '--create', 'feature')
    writeFiles(clone, {
      'calc/min.go': maxSource.replace('Max', 'Min').replace('a > b', 'a < b'),
      'calc/min_test.go': maxTest.replace('TestMax', 'TestMin').replace('Max(1, 2) != 2 || Max(2, 1) != 2', 'Min(1, 2) != 1 || Min(2, 1) != 1'),
    })
    git(clone, 'add', '--all')
    git(clone, '-c', 'user.email=e2e@example.com', '-c', 'user.name=e2e', 'commit', '--quiet', '--message', 'add Min')

    const { mutants } = await runMutants(clone, ['--operators', 'CONDITIONALS_NEGATION'])

    expect(mutants.map((m) => `${m.id} ${m.status}`)).toEqual(['calc/min.go:Min:CONDITIONALS_NEGATION#1 KILLED'])
  })

  it('--json and --stryker write the reports to files, and the rows still go to stdout', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    const out = scratchDir()

    const rows = await runCli(dir, ['run', '--base', 'HEAD', '--operators', 'CONDITIONALS_NEGATION', '--json', join(out, 'report.json'), '--stryker', join(out, 'stryker.json')])
    const json = JSON.parse(readFileSync(join(out, 'report.json'), 'utf8'))
    const stryker = JSON.parse(readFileSync(join(out, 'stryker.json'), 'utf8'))

    expect(rows.status).toBe(0)
    expect(rows.stdout).toMatch(/^mutants: 1, killed: 1 /m)
    expect(json.mutants.map((m: ReportedMutant) => `${m.id} ${m.status}`)).toEqual(['calc/calc.go:Max:CONDITIONALS_NEGATION#1 KILLED'])
    expect(stryker.files['calc/calc.go'].mutants.map((m: { status: string }) => m.status)).toEqual(['Killed'])
  })

  it('adds no entry to the build cache of go for the build of a mutant', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    // go keeps the index of a folder in its cache only when the files of the folder are 2 seconds old
    const hourAgo = new Date(Date.now() - 3_600_000)
    for (const path of ['calc/calc.go', 'calc/calc_test.go', 'calc']) {
      utimesSync(join(dir, path), hourAgo, hourAgo)
    }
    const cache = scratchDir()
    const entries = () => readdirSync(cache, { recursive: true }).filter((name) => String(name).endsWith('-a')).length
    const run = async (operators: string) => {
      const result = await runCli(dir, ['run', '--base', 'HEAD', '--format', 'json', '--operators', operators], { GOCACHE: cache })
      return verdicts(JSON.parse(result.stdout).mutants)
    }

    const first = await run('CONDITIONALS_NEGATION')
    const afterFirst = entries()
    const second = await run('CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION')

    expect(first).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
    expect(second).toEqual(['CONDITIONALS_BOUNDARY a > b LIVED', 'CONDITIONALS_NEGATION a > b KILLED'])
    expect(afterFirst).toBeGreaterThan(0)
    expect(entries()).toBe(afterFirst)
  })

  it('runs the mutants of an untracked file, and leaves git status as it was', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    const before = git(dir, 'status', '--porcelain', '--untracked-files=all')

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_NEGATION'])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
    expect(git(dir, 'status', '--porcelain', '--untracked-files=all')).toBe(before)
  })

  it('runs the tests of the package in a folder whose name differs from the package name', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'handlers/max.go': maxSource, 'handlers/max_test.go': maxTest })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_NEGATION'])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
  })

  it('runs a test file with a build tag only when --tags names the tag', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': `//go:build unit\n\n${maxTest}` })

    const withoutTags = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_NEGATION'])
    const withTags = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_NEGATION', '--tags', 'unit'])

    expect(verdicts(withoutTags.mutants)).toEqual(['CONDITIONALS_NEGATION a > b NOT COVERED'])
    expect(verdicts(withTags.mutants)).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
  })

  it('gives the same verdicts in two runs', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'accounts/accounts.go': accounts,
      'accounts/accounts_test.go': accountsTest,
      'total/total.go': total,
      'total/total_test.go': totalTest([3]),
    })

    const first = await runMutants(dir, ['--base', 'HEAD'])
    const second = await runMutants(dir, ['--base', 'HEAD'])

    expect(first.mutants.length).toBeGreaterThan(5)
    expect(second.mutants.map((m) => `${m.id} ${m.status}`)).toEqual(first.mutants.map((m) => `${m.id} ${m.status}`))
  })

  it('says so in one line when the changed files give no mutant', async () => {
    const dir = goRepository({ 'calc/calc.go': maxSource })
    writeFiles(dir, { 'calc/calc.go': maxWithComments })

    const result = await runCli(dir, ['run', '--base', 'HEAD'])

    expect(result.status).toBe(0)
    expect(result.stdout).toBe(`no mutant: 2 changed lines in 1 files (base ${git(dir, 'rev-parse', '--short=10', 'HEAD')})\n`)
  })

  it('with --format json and no mutant, writes only JSON on stdout and the count on stderr', async () => {
    const dir = goRepository({ 'calc/calc.go': maxSource })
    writeFiles(dir, { 'calc/calc.go': maxWithComments })

    const result = await runCli(dir, ['run', '--base', 'HEAD', '--format', 'json'])

    expect(result.status).toBe(0)
    expect(JSON.parse(result.stdout)).toEqual({ base: git(dir, 'rev-parse', 'HEAD'), mutants: [] })
    expect(result.stderr).toMatch(/^no mutant: 2 changed lines in 1 files/m)
  })

  it('takes the base, the tags and the operators from .mutants.yml', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'calc/calc.go': maxSource,
      'calc/calc_test.go': `//go:build unit\n\n${maxTest}`,
      '.mutants.yml': 'base: HEAD\ntags: [unit]\noperators: [CONDITIONALS_NEGATION]\n',
    })

    const { mutants } = await runMutants(dir, [])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
  })

  it('from a folder below the root, reads .mutants.yml at the root, and leaves out the files that exclude names', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'calc/calc.go': maxSource,
      'calc/calc_test.go': maxTest,
      'gen/max.go': maxSource.replace('package calc', 'package gen'),
      '.mutants.yml': 'base: HEAD\noperators: [CONDITIONALS_NEGATION]\nexclude: ["gen/**"]\n',
    })

    const { mutants } = await runMutants(join(dir, 'calc'), [])

    expect(mutants.map((m) => `${m.id} ${m.status}`)).toEqual(['calc/calc.go:Max:CONDITIONALS_NEGATION#1 KILLED'])
  })

  it('exits 2 for a key of .mutants.yml that it does not know', async () => {
    const dir = goRepository({ '.mutants.yml': 'workerz: 2\n' })

    const result = await runCli(dir, ['run'])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain('unknown key workerz in .mutants.yml (line 1)')
  })

  it('exits 124 at --limit, and still writes the report', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })

    const result = await runCli(dir, ['run', '--base', 'HEAD', '--format', 'json', '--limit', '1ms'])

    expect(result.status).toBe(124)
    expect(JSON.parse(result.stdout)).toMatchObject({ mutants: [] })
    expect(result.stderr).toContain('mutants stopped at the limit of 1ms')
  })

  it.each([
    [['--nope'], 'flag provided but not defined: -nope'],
    [['--format', 'xml'], '--format is rows or json, not "xml"'],
    [['--all'], '--all needs one FOLDER or more, and a FOLDER needs --all'],
    [['calc'], '--all needs one FOLDER or more, and a FOLDER needs --all'],
    [['--base', 'HEAD', '--proposals-anywhere'], '--proposals-anywhere needs --proposals'],
  ])('run %j exits 2 and says why', async (args, message) => {
    const result = await runCli(goRepository({ 'calc/calc.go': maxSource }), ['run', ...args])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain(message)
  })

  it('prints the survivors as rows, with the id that rerun takes', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 5) })

    const rows = await runCli(dir, ['run', '--base', 'HEAD', '--operators', 'NAMED_VALUE_SWAP'])
    const row = rows.stdout.split('\n').find((line) => line.includes('figures/figures.go:8 NAMED_VALUE_SWAP')) ?? ''
    const id = row.match(/\[(.+)\]$/)?.[1] ?? ''
    const again = await runCli(dir, ['rerun', id])

    expect(rows.status).toBe(10)
    expect(rows.stdout).toMatch(/^LIVED:$/m)
    expect(rows.stdout).toMatch(/^mutants: 1, lived: 1 /m)
    expect(id).toBe('figures/figures.go:Summarise:NAMED_VALUE_SWAP#1')
    expect(again.status).toBe(10)
    expect(again.stdout).toContain(`[${id}]`)
  })

  it('prints one row for a package with no test files, and keeps each mutant in the JSON', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource })
    const args = ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION']

    const rows = await runCli(dir, ['run', ...args])
    const json = await runMutants(dir, args)

    expect(rows.status).toBe(10)
    expect(rows.stdout).toMatch(/^ {2}package calc has no test files: 2 mutants$/m)
    expect(rows.stdout.match(/has no test files/g)).toHaveLength(1)
    expect(json.mutants.map((m) => `${m.operator} ${m.status}: ${m.detail}`)).toEqual([
      'CONDITIONALS_BOUNDARY NOT COVERED: package calc has no test files',
      'CONDITIONALS_NEGATION NOT COVERED: package calc has no test files',
    ])
  })
})

describe('mutants run --proposals', { timeout: 240_000 }, () => {
  const lives = {
    file: 'calc/calc.go',
    old: 'if a > b {',
    new: 'if a > b && a < 100 {',
    bug: 'a number of 100 or more is never the larger one',
  }
  const dies = { file: 'calc/calc.go', old: 'return b\n}', new: 'return a\n}', bug: 'the second number never wins' }
  const twice = { file: 'calc/calc.go', old: 'return', new: 'panic(0)', bug: 'Max never returns' }

  function proposalsFile(...proposals: object[]): string {
    const path = join(scratchDir(), 'proposals.jsonl')
    writeFileSync(path, proposals.map((p) => JSON.stringify(p)).join('\n'))
    return path
  }

  it('with --operators=none and --proposals-anywhere, runs only the proposals, also on committed lines, and returns the ref of each', async () => {
    const dir = goRepository({ 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    const file = proposalsFile({ ...lives, ref: 'finding-1' }, { ...lives, bug: 'the boundary moves', ref: 'finding-2' }, { ...twice, ref: 'finding-3' })

    const { result, mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators=none', '--proposals', file, '--proposals-anywhere'])
    const nothing = await runCli(dir, ['run', '--base', 'HEAD', '--operators=none'])

    expect(mutants.map((m) => `${m.operator} ${m.status} ${m.refs?.join(',')}`)).toEqual(['PROPOSED LIVED finding-1,finding-2'])
    expect(JSON.parse(result.stdout).proposals).toEqual({
      accepted: 2,
      rejected: [{ ...twice, ref: 'finding-3', reason: 'old found 2 times' }],
    })
    expect(nothing.status).toBe(2)
    expect(nothing.stderr).toContain('the run has no operator, no proposal and no check for caller gaps')
  })

  it('runs each proposal on a changed line, rejects each other one with its reason, and leaves git status as it was', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    const before = git(dir, 'status', '--porcelain', '--untracked-files=all')

    const { result, mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY', '--proposals', proposalsFile(lives, dies, twice)])

    expect(mutants.map((m) => `${m.operator} ${m.status} ${m.bug ?? ''}`.trim())).toEqual([
      'PROPOSED LIVED a number of 100 or more is never the larger one',
      'CONDITIONALS_BOUNDARY LIVED',
      'PROPOSED KILLED the second number never wins',
    ])
    expect(JSON.parse(result.stdout).proposals).toEqual({
      accepted: 2,
      rejected: [{ ...twice, reason: 'old found 2 times' }],
    })
    expect(result.status).toBe(10)
    expect(git(dir, 'status', '--porcelain', '--untracked-files=all')).toBe(before)
  })

  it('rerun finds a proposed mutant by its id without the file, and says when the proposal does not fit the code', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY', '--proposals', proposalsFile(lives)])
    const id = mutants.find((m) => m.operator === 'PROPOSED')?.id ?? ''

    const again = await runCli(dir, ['rerun', id])
    writeFiles(dir, { 'calc/calc.go': maxSource.replace('a > b', 'b < a') })
    const stale = await runCli(dir, ['rerun', id])

    expect(id).toMatch(/^calc\/calc\.go:Max:PROPOSED#\d{6}$/)
    expect(again.status).toBe(10)
    expect(again.stdout).toContain(`LIVED: calc/calc.go:4 PROPOSED: a number of 100 or more is never the larger one  [${id}]`)
    expect(stale.status).toBe(1)
    expect(stale.stderr).toContain('the proposal does not fit the code: old not found')
  })

  it('rerun of a proposed id that the store does not hold says that no mutant has the id', async () => {
    const dir = goRepository({ 'calc/calc.go': maxSource })

    const result = await runCli(dir, ['rerun', 'calc/calc.go:Max:PROPOSED#000000'])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain('no mutant has this id: calc/calc.go:Max:PROPOSED#000000')
  })

  it('exits 2 when the file of proposals does not exist', async () => {
    const dir = goRepository({ 'calc/calc.go': maxSource })

    const result = await runCli(dir, ['run', '--base', 'HEAD', '--proposals', join(dir, 'missing.jsonl')])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain('read the proposals')
  })
})

describe('mutants run --caller-gaps', { timeout: 240_000 }, () => {
  it('lists the changed statements of a package that no test of a changed caller with a fake runs, and exits 10 for the gaps alone', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'gate/gate.go': gate,
      'gate/gate_test.go': gateTest,
      'handler/handler.go': handler,
      'handler/handler_test.go': handlerTest,
    })

    const args = ['--base', 'HEAD', '--operators=none', '--caller-gaps']
    const rows = await runCli(dir, ['run', ...args])
    const json = await runMutants(dir, args)

    expect(rows.stdout).toContain(
      'CALLER GAPS:\n' +
        '  gate/gate.go:12 NewChecker, not run by the tests of handler\n' +
        '  gate/gate.go:16-17,19 (*Checker).Allow, not run by the tests of handler\n' +
        '  gate/gate.go:23-24,26 (*Checker).closing, not run by the tests of handler\n',
    )
    expect(rows.stdout).toContain('caller gaps: 3\n')
    expect(rows.status).toBe(10)
    expect(json.mutants).toEqual([])
    expect(JSON.parse(json.result.stdout).callerGaps).toEqual([
      { file: 'gate/gate.go', function: 'NewChecker', lines: [12], callers: ['handler'] },
      { file: 'gate/gate.go', function: '(*Checker).Allow', lines: [16, 17, 19], callers: ['handler'] },
      { file: 'gate/gate.go', function: '(*Checker).closing', lines: [23, 24, 26], callers: ['handler'] },
    ])
  })
})

describe('mutants rerun', { timeout: 240_000 }, () => {
  it('exits 10 for a mutant that lives, 0 for one that dies, and 2 for an id that names no mutant', async () => {
    const equal = goRepository({ 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 5) })
    const different = goRepository({ 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 7) })
    const id = 'figures/figures.go:Summarise:NAMED_VALUE_SWAP#1'

    const lives = await runCli(equal, ['rerun', id])
    const dies = await runCli(different, ['rerun', id])
    const unknown = await runCli(equal, ['rerun', 'figures/figures.go:Summarise:NAMED_VALUE_SWAP#2'])

    expect(lives.status).toBe(10)
    expect(lives.stdout).toContain(`LIVED: figures/figures.go:8 NAMED_VALUE_SWAP: paid, Owed: owed -> owed, Owed: paid  [${id}]`)
    expect(dies.status).toBe(0)
    expect(dies.stdout).toContain('KILLED: figures/figures.go:8 NAMED_VALUE_SWAP')
    expect(unknown.status).toBe(2)
    expect(unknown.stderr).toContain('no mutant has this id')
  })

  it('exits 1 for a mutant that gets no verdict, because it does not build', async () => {
    const dir = goRepository({ 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    const proposals = join(scratchDir(), 'proposals.jsonl')
    writeFileSync(proposals, JSON.stringify({ file: 'calc/calc.go', old: 'return b\n}', new: 'return "b"\n}', bug: 'Max returns text' }))
    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators=none', '--proposals', proposals, '--proposals-anywhere'])

    const rerun = await runCli(dir, ['rerun', mutants[0].id])

    expect(mutants.map((m) => m.status)).toEqual(['NOT VIABLE'])
    expect(rerun.status).toBe(1)
    expect(rerun.stdout).toMatch(/^NOT VIABLE: /)
  })

  it.each([
    [[], 'rerun needs one mutant id'],
    [['not-an-id'], 'is not a mutant id'],
    [['--nope'], 'flag provided but not defined: -nope'],
  ])('rerun %j exits 2 and says why', async (args, message) => {
    const result = await runCli(goRepository(), ['rerun', ...args])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain(message)
  })
})

describe('mutants operators', () => {
  it('lists each operator, with ERROR_CAUSE_REMOVE off by default', async () => {
    const result = await runCli(goRepository(), ['operators'])

    expect(result.status).toBe(0)
    expect(result.stdout).toMatch(/^NAMED_VALUE_SWAP\s+on\s+NAMED_VALUE_SWAP$/m)
    expect(result.stdout).toMatch(/^ERROR_CAUSE_REMOVE\s+off\s+ERROR_CAUSE_REMOVE$/m)
  })

  it('lists a rule of the repository with the file that holds it, also from a folder below the root', async () => {
    const dir = goRepository({
      '.mutants/operators/go/nil_map.yml': 'id: NIL_MAP\nlanguage: go\nmetadata: {default: off}\nrule:\n  pattern: map[$K]$V{}\nfix: nil\n',
      'calc/calc.go': 'package calc\n',
    })

    const result = await runCli(join(dir, 'calc'), ['operators'])

    expect(result.status).toBe(0)
    expect(result.stdout).toMatch(/^NIL_MAP\s+off\s+NIL_MAP \(from \.mutants\/operators\/go\/nil_map\.yml\)$/m)
  })
})
