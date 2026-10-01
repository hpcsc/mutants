import { describe, expect, it } from 'vitest'
import { buildTag, runCli, scratchDir } from '../testUtils'

describe('the root command', () => {
  it('lists the commands', async () => {
    const result = await runCli(scratchDir())

    expect(result.status).toBe(0)
    for (const command of ['run', 'rerun', 'operators', 'version', 'update']) {
      expect(result.stdout).toMatch(new RegExp(`^\\s+${command}\\s`, 'm'))
    }
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
