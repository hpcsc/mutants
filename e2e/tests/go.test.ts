import { existsSync, readdirSync, readFileSync, utimesSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { accounts, accountsTest, figures, figuresTest, maxSource, maxTest, total, totalTest } from '../fixtures'
import { goRepository, runCli, runMutants, scratchDir, startCli, verdicts, writeFiles } from '../testUtils'

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

const store = `package store

func Recover(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	return "message " + id, true
}

func Count() int {
	return 1
}
`

const storeTest = `package store

import "testing"

func TestCount(t *testing.T) {
	if Count() != 1 {
		t.Fatal("Count")
	}
}
`

const reactor = `package reactor

import "example.com/fixture/store"

func React(id string) string {
	text, ok := store.Recover(id)
	if !ok {
		return "nothing"
	}
	return text
}
`

const reactorTest = `package reactor

import "testing"

func TestReact(t *testing.T) {
	if React("a") != "message a" {
		t.Fatal("React")
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

describe('mutants run on Go', { timeout: 240_000 }, () => {
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

  it('a line whose only change is the amount of white space gives no mutant', async () => {
    const dir = goRepository({ 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    writeFiles(dir, { 'calc/calc.go': maxSource.replace('if a > b {', 'if a >  b {').replace('return a\n', 'return a + 0\n') })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY,ARITHMETIC_BASE'])

    expect(mutants.map((m) => `${m.operator} ${m.line}`)).toEqual(['ARITHMETIC_BASE 5'])
  })

  it('a mutant that only the tests of a changed caller run gets its verdict from those tests, also in rerun with the same base', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'store/store.go': store,
      'store/store_test.go': storeTest,
      'reactor/reactor.go': reactor,
      'reactor/reactor_test.go': reactorTest,
      'proposals.jsonl':
        JSON.stringify({ file: 'store/store.go', old: '"message " + id', new: 'id', bug: 'the text loses its start' }) +
        '\n' +
        JSON.stringify({ file: 'store/store.go', old: '{\n\t\treturn "", false\n\t}', new: '{}', bug: 'an empty id finds a message' }) +
        '\n',
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators=none', '--proposals', 'proposals.jsonl'])
    const [lives, dies] = mutants
    const killedAgain = await runCli(dir, ['rerun', '--base', 'HEAD', dies.id])
    const livesAgain = await runCli(dir, ['rerun', '--base', 'HEAD', lives.id])
    const withoutCallers = await runCli(dir, ['rerun', '--base', 'origin/HEAD', dies.id])

    expect(mutants.map((m) => `${m.bug} ${m.status} ${m.status === 'LIVED' ? m.detail : ''}`)).toEqual([
      'an empty id finds a message LIVED only the tests of reactor run it',
      'the text loses its start KILLED ',
    ])
    expect(dies.detail).toContain('--- FAIL: TestReact')
    expect(killedAgain.status).toBe(0)
    expect(livesAgain.status).toBe(10)
    expect(withoutCallers.status).toBe(10)
    expect(withoutCallers.stdout).toMatch(/^NOT COVERED: /)
    expect(withoutCallers.stderr).toContain('mutants runs no tests of a changed caller')
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
	// the runtime reports no deadlock while a timer waits, so a missing close makes the test hang
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
	// the runtime reports no deadlock while a timer waits, so a missing close makes the test hang
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
