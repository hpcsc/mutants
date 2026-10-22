import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { maxSource, maxTest } from '../fixtures'
import { elixirRepository, git, goRepository, mixProject, runCli, runMutants, scratchDir, verdicts, writeFiles } from '../testUtils'

const discount = `defmodule Shop.Discount do
  @limit 100

  def discount(total) do
    if total >= @limit, do: 10, else: 0
  end
end
`

const discountTest = `defmodule Shop.DiscountTest do
  use ExUnit.Case

  test "low", do: assert(Shop.Discount.discount(50) == 0)
  test "edge", do: assert(Shop.Discount.discount(100) == 10)
end
`

function totalsTest(paid: number, owed: number): string {
  return `defmodule Shop.TotalsTest do
  use ExUnit.Case

  test "summarise", do: assert(Shop.Totals.summarise(${paid}, ${owed}) == %{paid: ${paid}, owed: ${owed}})
end
`
}

const totals = `defmodule Shop.Totals do
  def summarise(paid, owed) do
    %{
      paid: paid, # what the customer paid
      owed: owed
    }
  end
end
`

const total = `defmodule Shop.Total do
  def total(items) do
    Enum.reduce(items, 0, fn item, sum -> sum + item end)
  end
end
`

function totalTest(items: number[]): string {
  return `defmodule Shop.TotalTest do
  use ExUnit.Case

  test "total", do: assert(Shop.Total.total([${items.join(', ')}]) == ${items.reduce((a, b) => a + b, 0)})
end
`
}

describe('mutants run on Elixir', { timeout: 240_000 }, () => {
  it('a swap of two keywords lives when the test uses two equal values, and dies when they differ', async () => {
    const equal = elixirRepository()
    writeFiles(equal, { 'lib/shop/totals.ex': totals, 'test/shop/totals_test.exs': totalsTest(5, 5) })
    const different = elixirRepository()
    writeFiles(different, { 'lib/shop/totals.ex': totals, 'test/shop/totals_test.exs': totalsTest(5, 3) })

    const lives = await runMutants(equal, ['--base', 'HEAD', '--operators', 'NAMED_VALUE_SWAP'])
    const dies = await runMutants(different, ['--base', 'HEAD', '--operators', 'NAMED_VALUE_SWAP'])

    expect(lives.mutants.map((m) => m.status)).toEqual(['LIVED'])
    expect(dies.mutants.map((m) => m.status)).toEqual(['KILLED'])
  })

  it('in a branch that no test enters, BRANCH_IF is not covered', async () => {
    const dir = elixirRepository()
    writeFiles(dir, {
      'lib/shop/accounts.ex': `defmodule Shop.Accounts do
  def load(account) do
    if account < 0 do
      raise ArgumentError, "negative"
    end

    account
  end
end
`,
      'test/shop/accounts_test.exs': 'defmodule Shop.AccountsTest do\n  use ExUnit.Case\n\n  test "load", do: assert(Shop.Accounts.load(1) == 1)\nend\n',
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'BRANCH_IF'])

    expect(verdicts(mutants)).toEqual(['BRANCH_IF raise ArgumentError, "negative" NOT COVERED'])
  })

  it('stopping a loop after its first item lives when the test has one item, and dies when it has two', async () => {
    const one = elixirRepository()
    writeFiles(one, { 'lib/shop/total.ex': total, 'test/shop/total_test.exs': totalTest([3]) })
    const two = elixirRepository()
    writeFiles(two, { 'lib/shop/total.ex': total, 'test/shop/total_test.exs': totalTest([3, 4]) })

    const lives = await runMutants(one, ['--base', 'HEAD', '--operators', 'BREAK_AT_END'])
    const dies = await runMutants(two, ['--base', 'HEAD', '--operators', 'BREAK_AT_END'])

    expect(lives.mutants.map((m) => m.status)).toEqual(['LIVED'])
    expect(dies.mutants.map((m) => m.status)).toEqual(['KILLED'])
  })

  it('a proposal that does not compile is not viable, and rerun of it exits 1', async () => {
    const dir = elixirRepository()
    writeFiles(dir, {
      'lib/shop/discount.ex': discount,
      'test/shop/discount_test.exs': discountTest,
      'bugs.jsonl': `${JSON.stringify({ file: 'lib/shop/discount.ex', old: 'do: 10', new: 'do: 10 +', bug: 'the discount does not parse' })}\n`,
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'none', '--proposals', 'bugs.jsonl'])
    const rerun = await runCli(dir, ['rerun', mutants[0].id])

    expect(mutants.map((m) => m.status)).toEqual(['NOT VIABLE'])
    expect(mutants[0].detail).toContain('the mutant does not compile')
    expect(rerun.status).toBe(1)
  })

  it('an endless recursion times out, and the port program of a test of a mutant stops with its VM', async () => {
    const pids = join(scratchDir(), 'children.pid')
    const dir = elixirRepository()
    writeFiles(dir, {
      'lib/shop/count.ex': `defmodule Shop.Count do
  def count(n), do: count(0, n)

  defp count(i, n) when i < n, do: count(i + 1, n)
  defp count(i, _n), do: i
end
`,
      'test/shop/count_test.exs': `defmodule Shop.CountTest do
  use ExUnit.Case

  test "count" do
    port = Port.open({:spawn, "cat"}, [])
    {:os_pid, pid} = Port.info(port, :os_pid)
    File.write!(System.fetch_env!("E2E_PIDS"), "#{pid}\\n", [:append])
    assert Shop.Count.count(3) == 3
  end
end
`,
    })

    const result = await runCli(dir, ['run', '--format', 'json', '--base', 'HEAD', '--operators', 'ARITHMETIC_BASE'], { E2E_PIDS: pids })

    expect(verdicts(JSON.parse(result.stdout).mutants)).toEqual(['ARITHMETIC_BASE i + 1 TIMED OUT'])
    const children = readFileSync(pids, 'utf8').trim().split('\n').map(Number)
    expect(children.length).toBeGreaterThan(1)
    await expect.poll(() => children.filter(isAlive), { timeout: 10_000 }).toEqual([])
  })

  it('with elixir.reuse_vm, gives each mutant the verdict of a new VM, and no VM stays alive after the run', async () => {
    const pids = join(scratchDir(), 'vms.pid')
    const dir = elixirRepository()
    const recordPid = '  setup_all do\n    File.write!(System.fetch_env!("E2E_PIDS"), System.pid() <> "\\n", [:append])\n    :ok\n  end\n\n'
    writeFiles(dir, {
      'lib/shop/discount.ex': discount,
      'test/shop/discount_test.exs': discountTest.replace('  use ExUnit.Case\n\n', `  use ExUnit.Case\n\n${recordPid}`),
      'lib/shop/total.ex': total,
      'test/shop/total_test.exs': totalTest([3]),
    })
    const statuses = (stdout: string) => JSON.parse(stdout).mutants.map((m: { id: string; status: string }) => `${m.id} ${m.status}`)

    const plain = await runCli(dir, ['run', '--format', 'json', '--base', 'HEAD'], { E2E_PIDS: pids })
    writeFiles(dir, { '.mutants.yml': 'elixir:\n  reuse_vm: true\n' })
    const reused = await runCli(dir, ['run', '--format', 'json', '--base', 'HEAD'], { E2E_PIDS: pids })

    expect(statuses(reused.stdout)).toEqual(statuses(plain.stdout))
    expect(statuses(reused.stdout)).toContain('lib/shop/discount.ex:Shop.Discount.discount/1:CONDITIONALS_BOUNDARY#1 KILLED')
    const vms = [...new Set(readFileSync(pids, 'utf8').trim().split('\n').map(Number))]
    await expect.poll(() => vms.filter(isAlive), { timeout: 10_000 }).toEqual([])
  })

  it('runs the mutants of an untracked file, and adds nothing to git status but the build in _build', async () => {
    const dir = elixirRepository()
    writeFiles(dir, { 'lib/shop/discount.ex': discount, 'test/shop/discount_test.exs': discountTest })
    const before = git(dir, 'status', '--porcelain', '--ignored').split('\n')

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_BOUNDARY total >= @limit KILLED'])
    const added = git(dir, 'status', '--porcelain', '--ignored')
      .split('\n')
      .filter((line) => !before.includes(line))
    expect(added).toEqual(['!! _build/'])
  })

  it('a project that does not compile stops the run with exit 2, and the message names the project', async () => {
    const dir = elixirRepository()
    writeFiles(dir, {
      'lib/shop/discount.ex': discount,
      'lib/shop/broken.ex': 'defmodule Shop.Broken do\n  def one, do: two()\nend\n',
      'test/shop/discount_test.exs': discountTest,
    })

    const result = await runCli(dir, ['run', '--base', 'HEAD'])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain('the Elixir project in . does not compile')
  })

  it('tests that fail with the real code stop the run with exit 2, and the message names the project', async () => {
    const dir = elixirRepository()
    writeFiles(dir, { 'lib/shop/discount.ex': discount, 'test/shop/discount_test.exs': discountTest.replace('== 10', '== 11') })

    const result = await runCli(dir, ['run', '--base', 'HEAD'])

    expect(result.status).toBe(2)
    expect(result.stderr).toContain('the tests of the Elixir project in . fail with the real code')
  })

  it('runs the tests of the project of a file in its own folder', async () => {
    const dir = elixirRepository()
    writeFiles(dir, {
      'services/shop/mix.exs': mixProject,
      'services/shop/.gitignore': '/_build/\n',
      'services/shop/lib/shop/discount.ex': discount,
      'services/shop/test/test_helper.exs': 'ExUnit.start()\n',
      'services/shop/test/shop/discount_test.exs': discountTest,
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    expect(mutants.map((m) => `${m.file} ${m.status}`)).toEqual(['services/shop/lib/shop/discount.ex KILLED'])
  })

  it('makes no mutant in a call of Logger or in a typespec', async () => {
    const dir = elixirRepository()
    writeFiles(dir, {
      'lib/shop/level.ex': `defmodule Shop.Level do
  require Logger

  @spec level(1 | 2) :: integer
  def level(n) do
    Logger.info("level #{n + 1}")
    n + 3
  end
end
`,
      'test/shop/level_test.exs': 'defmodule Shop.LevelTest do\n  use ExUnit.Case\n\n  test "level", do: assert(Shop.Level.level(1) == 4)\nend\n',
    })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'INTEGER_INCREMENT'])

    expect(verdicts(mutants)).toEqual(['INTEGER_INCREMENT 3 KILLED'])
  })

  it('rerun gives an Elixir mutant the verdict of the run, and exits 10 when it lives', async () => {
    const dir = elixirRepository()
    writeFiles(dir, { 'lib/shop/discount.ex': discount, 'test/shop/discount_test.exs': discountTest.replace('discount(100)', 'discount(150)') })
    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY'])

    const rerun = await runCli(dir, ['rerun', mutants[0].id])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_BOUNDARY total >= @limit LIVED'])
    expect(rerun.status).toBe(10)
    expect(rerun.stdout).toContain(`LIVED: lib/shop/discount.ex:5 CONDITIONALS_BOUNDARY`)
  })
})

describe('mutants run on Go and Elixir', { timeout: 240_000 }, () => {
  it('runs the mutants of the Go files and of the Elixir files in one run, and the Stryker report gives the language of each file', async () => {
    const dir = goRepository({ 'mix.exs': mixProject, '.gitignore': '/_build/\n', 'test/test_helper.exs': 'ExUnit.start()\n' })
    writeFiles(dir, {
      'calc/calc.go': maxSource,
      'calc/calc_test.go': maxTest,
      'lib/shop/discount.ex': discount,
      'test/shop/discount_test.exs': discountTest,
    })
    const stryker = join(scratchDir(), 'stryker.json')

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_BOUNDARY', '--stryker', stryker])

    expect([...new Set(mutants.map((m) => m.file))]).toEqual(['calc/calc.go', 'lib/shop/discount.ex'])
    const languages = Object.entries(JSON.parse(readFileSync(stryker, 'utf8')).files).map(
      ([file, entry]) => `${file} ${(entry as { language: string }).language}`,
    )
    expect(languages).toEqual(['calc/calc.go go', 'lib/shop/discount.ex elixir'])
  })

  it('leaves the Elixir files out of the run when .mutants.yml excludes them', async () => {
    const dir = goRepository({ 'mix.exs': mixProject, '.gitignore': '/_build/\n', 'test/test_helper.exs': 'ExUnit.start()\n' })
    writeFiles(dir, {
      '.mutants.yml': 'exclude: ["**/*.ex"]\n',
      'calc/calc.go': maxSource,
      'calc/calc_test.go': maxTest,
      'lib/shop/discount.ex': discount,
      'test/shop/discount_test.exs': discountTest,
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
