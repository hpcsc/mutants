import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { buildTag, git, goRepository, runCli, scratchDir } from '../testUtils'

describe('the root command', () => {
  it('lists the commands', async () => {
    const result = await runCli(scratchDir())

    expect(result.status).toBe(0)
    for (const command of ['run', 'rerun', 'operators', 'config', 'version', 'update']) {
      expect(result.stdout).toMatch(new RegExp(`^\\s+${command}\\s`, 'm'))
    }
  })
})

describe('mutants config init', () => {
  it('writes .mutants.yml with the build tag of the tests, and does not replace it', async () => {
    const dir = goRepository({
      'calc/calc.go': 'package calc\n\nfunc One() int { return 1 }\n',
      'calc/calc_test.go': '//go:build unit\n\npackage calc\n',
    })

    const first = await runCli(dir, ['config', 'init'])
    const written = readFileSync(join(dir, '.mutants.yml'), 'utf8')
    const second = await runCli(dir, ['config', 'init'])

    expect(first.status).toBe(0)
    expect(written).toContain('\ntags: [unit]\n')
    expect(written).toContain('\n# base: origin/HEAD\n')
    expect(second.status).toBe(2)
    expect(second.stderr).toContain('does not replace it')
    expect(readFileSync(join(dir, '.mutants.yml'), 'utf8')).toBe(written)
  })

  it('in a folder below the root of a clone, writes .mutants.yml at the root with the default branch of origin as the base', async () => {
    const clone = scratchDir()
    git(clone, 'clone', '--quiet', goRepository({ 'calc/calc.go': 'package calc\n' }), '.')

    const result = await runCli(join(clone, 'calc'), ['config', 'init'])

    expect(result.status).toBe(0)
    expect(readFileSync(join(clone, '.mutants.yml'), 'utf8')).toMatch(/^base: origin\/main$/m)
  })
})

describe('mutants version', () => {
  it('prints the tag the binary was built with', async () => {
    const result = await runCli(scratchDir(), ['version'])

    expect(result.status).toBe(0)
    expect(result.stdout.trim()).toBe(buildTag())
  })

  it('--version prints the name and the tag', async () => {
    const result = await runCli(scratchDir(), ['--version'])

    expect(result.status).toBe(0)
    expect(result.stdout.trim()).toBe(`mutants version ${buildTag()}`)
  })
})
