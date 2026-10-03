import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { accounts, accountsTest, figures, figuresTest, maxSource, maxTest, total, totalTest } from '../fixtures'
import { git, goRepository, type ReportedMutant, runCli, runMutants, scratchDir, verdicts, writeFiles } from '../testUtils'

const maxWithComments = `// Package calc compares numbers.
// It has no state.
${maxSource}`

describe('mutants run', { timeout: 240_000 }, () => {
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

  it('runs the mutants of an untracked file, and leaves git status as it was', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })
    const before = git(dir, 'status', '--porcelain', '--untracked-files=all')

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', '--operators', 'CONDITIONALS_NEGATION'])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
    expect(git(dir, 'status', '--porcelain', '--untracked-files=all')).toBe(before)
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
      '.mutants.yml': 'base: HEAD\noperators: [CONDITIONALS_NEGATION]\ngo:\n  tags: [unit]\n',
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

  it('in a linked work tree with no .mutants.yml, reads mutants.yml in the git folder that the work trees share', async () => {
    const dir = goRepository()
    const linked = join(scratchDir(), 'linked')
    git(dir, 'worktree', 'add', '--quiet', linked)
    writeFiles(dir, { '.git/mutants.yml': 'base: HEAD\noperators: [CONDITIONALS_NEGATION]\n' })
    writeFiles(linked, { 'calc/calc.go': maxSource, 'calc/calc_test.go': maxTest })

    const { mutants } = await runMutants(linked, [])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
  })

  it('reads .mutants.yml and ignores mutants.yml in the git folder, and says so on stderr', async () => {
    const dir = goRepository()
    writeFiles(dir, {
      'calc/calc.go': maxSource,
      'calc/calc_test.go': maxTest,
      '.mutants.yml': 'base: HEAD\noperators: [CONDITIONALS_NEGATION]\n',
      '.git/mutants.yml': 'workerz: 2\n',
    })

    const { result, mutants } = await runMutants(dir, [])

    expect(verdicts(mutants)).toEqual(['CONDITIONALS_NEGATION a > b KILLED'])
    expect(result.stderr).toContain('mutants ignores .git/mutants.yml, because .mutants.yml exists')
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

  it('exits 2 outside a git repository and for a key of .mutants.yml that it does not know, as run does', async () => {
    const id = 'calc/calc.go:Max:CONDITIONALS_NEGATION#1'

    const outside = await runCli(scratchDir(), ['rerun', id])
    const unknownKey = await runCli(goRepository({ '.mutants.yml': 'workerz: 2\n' }), ['rerun', id])

    expect(outside.status).toBe(2)
    expect(outside.stderr).toContain('is not in a git repository')
    expect(unknownKey.status).toBe(2)
    expect(unknownKey.stderr).toContain('unknown key workerz in .mutants.yml (line 1)')
  })
})

describe('mutants operators', () => {
  it('lists each operator of each language, with ERROR_CAUSE_REMOVE off by default', async () => {
    const result = await runCli(goRepository(), ['operators'])

    expect(result.status).toBe(0)
    expect(result.stdout).toMatch(/^go\s+NAMED_VALUE_SWAP\s+on\s+NAMED_VALUE_SWAP$/m)
    expect(result.stdout).toMatch(/^go\s+ERROR_CAUSE_REMOVE\s+off\s+ERROR_CAUSE_REMOVE$/m)
    expect(result.stdout).toMatch(/^python\s+NAMED_VALUE_SWAP\s+on\s+NAMED_VALUE_SWAP$/m)
  })

  it('lists a rule of the repository with the file that holds it, also from a folder below the root', async () => {
    const dir = goRepository({
      '.mutants/operators/go/nil_map.yml': 'id: NIL_MAP\nlanguage: go\nmetadata: {default: off}\nrule:\n  pattern: map[$K]$V{}\nfix: nil\n',
      'calc/calc.go': 'package calc\n',
    })

    const result = await runCli(join(dir, 'calc'), ['operators'])

    expect(result.status).toBe(0)
    expect(result.stdout).toMatch(/^go\s+NIL_MAP\s+off\s+NIL_MAP \(from \.mutants\/operators\/go\/nil_map\.yml\)$/m)
  })
})
