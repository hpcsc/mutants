import { readFileSync, symlinkSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { maxSource, maxTest } from '../fixtures'
import {
  git,
  goRepository,
  pyproject,
  pythonRepository,
  pythonVenv,
  runCli,
  runMutants,
  scratchDir,
  verdicts,
  writeFiles,
} from '../testUtils'

const discount = `LIMIT = 100


def discount(total):
    if total >= LIMIT:
        return 10
    return 0
`

const discountTest = `from shop.discount import discount


def test_low():
    assert discount(50) == 0


def test_edge():
    assert discount(100) == 10
`

function totalsTest(paid: number, owed: number): string {
  return `from shop.totals import summarise


def test_summarise():
    assert summarise(${paid}, ${owed}) == {"paid": ${paid}, "owed": ${owed}}
`
}

const totals = `def summarise(paid, owed):
    return dict(
        paid=paid,  # what the customer paid
        owed=owed,
    )
`

const total = `def total(items):
    s = 0
    for item in items:
        s += item
    return s
`

function totalTest(items: number[]): string {
  return `from shop.total import total


def test_total():
    assert total([${items.join(', ')}]) == ${items.reduce((a, b) => a + b, 0)}
`
}

describe('mutants run on Python', { timeout: 240_000 }, () => {
  it('a swap of two keyword arguments lives when the test uses two equal values, and dies when they differ', async () => {
    const equal = pythonRepository()
    writeFiles(equal, { 'shop/totals.py': totals, 'tests/test_totals.py': totalsTest(5, 5) })
    const different = pythonRepository()
    writeFiles(different, { 'shop/totals.py': totals, 'tests/test_totals.py': totalsTest(5, 3) })

    const lives = await runMutants(equal, ['--base', 'HEAD', '--operators', 'NAMED_VALUE_SWAP'])
    const dies = await runMutants(different, ['--base', 'HEAD', '--operators', 'NAMED_VALUE_SWAP'])

    expect(lives.mutants.map((m) => m.status)).toEqual(['LIVED'])
    expect(dies.mutants.map((m) => m.status)).toEqual(['KILLED'])
  })

  it('in a branch that no test enters, BRANCH_IF is not covered', async () => {
    const dir = pythonRepository()
    writeFiles(dir, {
      'shop/accounts.py': 'def load(account):\n    if account < 0:\n        raise ValueError("negative")\n    return account\n',
      'tests/test_accounts.py': 'from shop.accounts import load\n\n\ndef test_load():\n    assert load(1) == 1\n',
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'BRANCH_IF'])

    expect(verdicts(mutants)).toEqual(['BRANCH_IF raise ValueError("negative") NOT COVERED'])
  })

  it('a mutant runs only the tests that run its line', async () => {
    const log = join(scratchDir(), 'other.log')
    const dir = pythonRepository()
    writeFiles(dir, {
      'shop/discount.py': discount,
      'tests/test_discount.py': discountTest,
      'tests/test_other.py': 'import os\n\n\ndef test_other():\n    with open(os.environ["E2E_LOG"], "a") as log:\n        log.write("ran\\n")\n',
    })

    const result = await runCli(dir, ['run', '--format', 'json', '--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'], { E2E_LOG: log })

    expect(result.status).toBe(0)
    expect(readFileSync(log, 'utf8')).toBe('ran\n')
  })

  it('stopping a loop after its first item lives when the test has one item, and dies when it has two', async () => {
    const one = pythonRepository()
    writeFiles(one, { 'shop/total.py': total, 'tests/test_total.py': totalTest([3]) })
    const two = pythonRepository()
    writeFiles(two, { 'shop/total.py': total, 'tests/test_total.py': totalTest([3, 4]) })

    const lives = await runMutants(one, ['--base', 'HEAD', '--operators', 'BREAK_AT_END'])
    const dies = await runMutants(two, ['--base', 'HEAD', '--operators', 'BREAK_AT_END'])

    expect(lives.mutants.map((m) => m.status)).toEqual(['LIVED'])
    expect(dies.mutants.map((m) => m.status)).toEqual(['KILLED'])
  })

  it('a mutant that breaks the import of its module dies with the error of the collection', async () => {
    const dir = pythonRepository()
    writeFiles(dir, {
      'shop/names.py': 'NAMES = ["a", "b"]\nLAST = NAMES[1]\n',
      'tests/test_names.py': 'from shop.names import LAST\n\n\ndef test_last():\n    assert LAST == "b"\n',
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'INTEGER_INCREMENT'])

    expect(verdicts(mutants)).toEqual(['INTEGER_INCREMENT 1 KILLED'])
    expect(mutants[0].detail).toContain('IndexError')
  })

  it('a proposal that does not compile is not viable, and rerun of it exits 1', async () => {
    const dir = pythonRepository()
    writeFiles(dir, {
      'shop/discount.py': discount,
      'tests/test_discount.py': discountTest,
      'bugs.jsonl': `${JSON.stringify({ file: 'shop/discount.py', old: 'return 10', new: 'return 10 +', bug: 'the discount does not parse' })}\n`,
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'none', '--proposals', 'bugs.jsonl'])
    const rerun = await runCli(dir, ['rerun', mutants[0].id])

    expect(mutants.map((m) => m.status)).toEqual(['NOT VIABLE'])
    expect(mutants[0].detail).toContain('the mutant does not compile')
    expect(rerun.status).toBe(1)
  })

  it('an endless loop times out, and no process that a test of a mutant starts stays alive', async () => {
    const pids = join(scratchDir(), 'children.pid')
    const dir = pythonRepository()
    writeFiles(dir, {
      'shop/count.py': 'def count(n):\n    c = 0\n    i = 0\n    while i < n:\n        c = c + 1\n        i += 1\n    return c\n',
      'tests/test_count.py': `import os
import subprocess

from shop.count import count


def test_count():
    child = subprocess.Popen(["sleep", "120"])
    with open(os.environ["E2E_PIDS"], "a") as pids:
        pids.write(f"{child.pid}\\n")
    assert count(3) == 3
`,
    })

    const result = await runCli(dir, ['run', '--format', 'json', '--base', 'HEAD', '--operators', 'INCREMENT_DECREMENT'], { E2E_PIDS: pids })

    expect(verdicts(JSON.parse(result.stdout).mutants)).toEqual(['INCREMENT_DECREMENT i += 1 TIMED OUT'])
    const children = readFileSync(pids, 'utf8').trim().split('\n').map(Number)
    expect(children.length).toBeGreaterThan(1)
    await expect.poll(() => children.filter(isAlive), { timeout: 10_000 }).toEqual([])
  })

  it('runs the mutants of an untracked file, and leaves git status as it was', async () => {
    const dir = pythonRepository()
    writeFiles(dir, { 'shop/discount.py': discount, 'tests/test_discount.py': discountTest })
    const before = git(dir, 'status', '--porcelain', '--ignored')

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_BOUNDARY total >= LIMIT KILLED'])
    expect(git(dir, 'status', '--porcelain', '--ignored')).toBe(before)
  })

  it('a project without coverage.py stops the run with exit 2, and the message tells how to add it', async () => {
    const dir = pythonRepository({}, pythonVenv(false))
    writeFiles(dir, { 'shop/discount.py': discount, 'tests/test_discount.py': discountTest })

    const result = await runCli(dir, ['run', '--base', 'HEAD'])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain('the Python project in . has no coverage.py: add coverage or pytest-cov to its dev dependencies')
  })

  it('tests that fail with the real code stop the run with exit 2, and the message names the project', async () => {
    const dir = pythonRepository()
    writeFiles(dir, { 'shop/discount.py': discount, 'tests/test_discount.py': discountTest.replace('== 10', '== 11') })

    const result = await runCli(dir, ['run', '--base', 'HEAD'])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain('the tests of the Python project in . fail with the real code')
  })

  it('runs the tests of the project of a file in its own folder, with its own venv', async () => {
    const dir = pythonRepository()
    writeFiles(dir, {
      'services/api/pyproject.toml': pyproject,
      'services/api/shop/discount.py': discount,
      'services/api/tests/test_discount.py': discountTest,
    })
    symlinkSync(pythonVenv(), join(dir, 'services/api/.venv'))

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    expect(mutants.map((m) => `${m.file} ${m.status}`)).toEqual(['services/api/shop/discount.py KILLED'])
  })

  it('makes no mutant in a call of a logger or in a type annotation', async () => {
    const dir = pythonRepository()
    writeFiles(dir, {
      'shop/level.py': `import logging
from typing import Literal

logger = logging.getLogger(__name__)


def level(n: Literal[1, 2]) -> int:
    logger.info("level %d", n + 1)
    return n + 3
`,
      'tests/test_level.py': 'from shop.level import level\n\n\ndef test_level():\n    assert level(1) == 4\n',
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'INTEGER_INCREMENT'])

    expect(verdicts(mutants)).toEqual(['INTEGER_INCREMENT 3 KILLED'])
  })

  it('rerun gives a Python mutant the verdict of the run, and exits 10 when it lives', async () => {
    const dir = pythonRepository()
    writeFiles(dir, { 'shop/discount.py': discount, 'tests/test_discount.py': discountTest.replace('discount(100)', 'discount(150)') })
    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    const rerun = await runCli(dir, ['rerun', mutants[0].id])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_BOUNDARY total >= LIMIT LIVED'])
    expect(rerun.status).toBe(10)
    expect(rerun.stdout).toContain(`LIVED: shop/discount.py:5 CONDITIONALS_BOUNDARY`)
  })
})

describe('mutants run on Go and Python', { timeout: 240_000 }, () => {
  it('runs the mutants of the Go files and of the Python files in one run, and the Stryker report gives the language of each file', async () => {
    const dir = goRepository({ 'pyproject.toml': pyproject, '.gitignore': '.venv\n' })
    symlinkSync(pythonVenv(), join(dir, '.venv'))
    writeFiles(dir, {
      'calc/calc.go': maxSource,
      'calc/calc_test.go': maxTest,
      'shop/discount.py': discount,
      'tests/test_discount.py': discountTest,
    })
    const stryker = join(scratchDir(), 'stryker.json')

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY', '--stryker', stryker])

    expect([...new Set(mutants.map((m) => m.file))]).toEqual(['calc/calc.go', 'shop/discount.py'])
    const languages = Object.entries(JSON.parse(readFileSync(stryker, 'utf8')).files).map(
      ([file, entry]) => `${file} ${(entry as { language: string }).language}`,
    )
    expect(languages).toEqual(['calc/calc.go go', 'shop/discount.py python'])
  })

  it('leaves the Python files out of the run when .mutants.yml excludes them', async () => {
    const dir = goRepository({ 'pyproject.toml': pyproject, '.gitignore': '.venv\n' })
    symlinkSync(pythonVenv(), join(dir, '.venv'))
    writeFiles(dir, {
      '.mutants.yml': 'exclude: ["**/*.py"]\n',
      'calc/calc.go': maxSource,
      'calc/calc_test.go': maxTest,
      'shop/discount.py': discount,
      'tests/test_discount.py': discountTest,
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    expect([...new Set(mutants.map((m) => m.file))]).toEqual(['calc/calc.go'])
  })
})

function isAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}
