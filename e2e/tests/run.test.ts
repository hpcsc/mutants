import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { git, goRepository, runCli, runMutants, verdicts, writeFiles } from '../testUtils'

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

describe('mutants run', { timeout: 240_000 }, () => {
  it('a swap of two fields lives when the test uses two equal values, and dies when they differ', async () => {
    const equal = goRepository()
    writeFiles(equal, { 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 5) })
    const different = goRepository()
    writeFiles(different, { 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 7) })

    const lives = await runMutants(equal, ['--base', 'HEAD', '--operators', 'SWAP_FIELDS'])
    const dies = await runMutants(different, ['--base', 'HEAD', '--operators', 'SWAP_FIELDS'])

    expect(verdicts(lives.mutants)).toEqual(['SWAP_FIELDS paid, Owed: owed LIVED'])
    expect(lives.result.status).toBe(10)
    expect(verdicts(dies.mutants)).toEqual(['SWAP_FIELDS paid, Owed: owed KILLED'])
    expect(dies.result.status).toBe(0)
  })

  it('in an error branch that no test enters, BRANCH_IF lives and the return in it is not covered', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'accounts/accounts.go': accounts, 'accounts/accounts_test.go': accountsTest })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'BRANCH_IF,RETURN_ERROR_NIL'])

    expect(mutants.map((m) => `${m.operator} ${m.line} ${m.status}`)).toEqual(['BRANCH_IF 6 LIVED', 'RETURN_ERROR_NIL 7 NOT COVERED'])
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

    const lives = await runMutants(around, ['--base', 'HEAD', '--operators', 'TIME_BOUNDARY'])
    const dies = await runMutants(at, ['--base', 'HEAD', '--operators', 'TIME_BOUNDARY'])

    expect(verdicts(lives.mutants)).toEqual(['TIME_BOUNDARY now.After(deadline) LIVED'])
    expect(verdicts(dies.mutants)).toEqual(['TIME_BOUNDARY now.After(deadline) KILLED'])
  })

  it('24 hours in place of a calendar day live when the test has no change of daylight saving time, and die when it has one', async () => {
    const utc = goRepository()
    writeFiles(utc, { 'due/due.go': due, 'due/due_test.go': dueTest('UTC') })
    const sydney = goRepository()
    writeFiles(sydney, { 'due/due.go': due, 'due/due_test.go': dueTest('Australia/Sydney') })

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

  it('a busy loop and a removed close time out, and no process of their tests stays alive', async () => {
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
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestCount(t *testing.T) {
	child := exec.Command("sleep", "120")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("child.pid", []byte(strconv.Itoa(child.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
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
    const child = Number(readFileSync(join(dir, 'count', 'child.pid'), 'utf8'))
    await vi.waitFor(
      () => {
        expect(() => process.kill(child, 0)).toThrow()
      },
      { timeout: 5_000, interval: 100 },
    )
  })

  it('finds the same mutants whether git uses mnemonic prefixes or not', async () => {
    const changed = async (mnemonicPrefix: string) => {
      const dir = goRepository({ 'calc/calc.go': maxSource.replace('a > b', 'a == b') })
      git(dir, 'config', 'diff.mnemonicPrefix', mnemonicPrefix)
      writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
      const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION'])
      return mutants.map((m) => `${m.id} ${m.status}`)
    }

    const withoutPrefixes = await changed('false')
    const withPrefixes = await changed('true')

    expect(withoutPrefixes).toEqual(['calc/calc.go:Max:CONDITIONALS_BOUNDARY#1 LIVED', 'calc/calc.go:Max:CONDITIONALS_NEGATION#1 KILLED'])
    expect(withPrefixes).toEqual(withoutPrefixes)
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

    const result = await runCli(dir, ['run', '--base', 'HEAD'])

    expect(result.status).toBe(0)
    expect(result.stdout).toMatch(/^no mutant: 0 changed lines in 0 files \(base [0-9a-f]{10}\)\n$/)
  })

  it('prints the survivors as rows, with the id that rerun takes', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 5) })

    const result = await runCli(dir, ['run', '--base', 'HEAD', '--operators', 'SWAP_FIELDS'])

    expect(result.status).toBe(10)
    expect(result.stdout).toMatch(
      /^LIVED:\n {2}figures\/figures.go:8 SWAP_FIELDS: paid, Owed: owed -> owed, Owed: paid {2}\[figures\/figures.go:Summarise:SWAP_FIELDS#1\]\nmutants: 1, lived: 1 \(base [0-9a-f]{10}\)\n$/,
    )
  })

  it('prints one row for a package with no test files, and keeps each mutant in the JSON', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource })
    const args = ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY,CONDITIONALS_NEGATION']

    const rows = await runCli(dir, ['run', ...args])
    const json = await runMutants(dir, args)

    expect(rows.status).toBe(10)
    expect(rows.stdout).toMatch(/^NOT COVERED:\n {2}package calc has no test files: 2 mutants\nmutants: 2, not covered: 2 \(base [0-9a-f]{10}\)\n$/)
    expect(json.mutants.map((m) => `${m.operator} ${m.status}: ${m.detail}`)).toEqual([
      'CONDITIONALS_BOUNDARY NOT COVERED: package calc has no test files',
      'CONDITIONALS_NEGATION NOT COVERED: package calc has no test files',
    ])
  })
})

describe('mutants rerun', { timeout: 240_000 }, () => {
  it('exits 10 for a mutant that lives, 0 for one that dies, and 2 for an id that names no mutant', async () => {
    const equal = goRepository({ 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 5) })
    const different = goRepository({ 'figures/figures.go': figures, 'figures/figures_test.go': figuresTest(5, 7) })
    const id = 'figures/figures.go:Summarise:SWAP_FIELDS#1'

    const lives = await runCli(equal, ['rerun', id])
    const dies = await runCli(different, ['rerun', id])
    const unknown = await runCli(equal, ['rerun', 'figures/figures.go:Summarise:SWAP_FIELDS#2'])

    expect(lives.status).toBe(10)
    expect(lives.stdout).toContain(`LIVED: figures/figures.go:8 SWAP_FIELDS: paid, Owed: owed -> owed, Owed: paid  [${id}]`)
    expect(dies.status).toBe(0)
    expect(dies.stdout).toContain('KILLED: figures/figures.go:8 SWAP_FIELDS')
    expect(unknown.status).toBe(2)
    expect(unknown.stderr).toContain('no mutant has this id')
  })
})

describe('mutants operators', () => {
  it('lists each operator, with ERRORF_WRAP off by default', async () => {
    const result = await runCli(goRepository(), ['operators'])

    expect(result.status).toBe(0)
    expect(result.stdout).toMatch(/^SWAP_FIELDS\s+on\s+SWAP_FIELDS$/m)
    expect(result.stdout).toMatch(/^ERRORF_WRAP\s+off\s+ERRORF_WRAP$/m)
  })
})
