import { describe, expect, it } from 'vitest'
import { buildTag, openCli, runCli, scratchDir } from '../testUtils'

describe('the root command', () => {
  it('greets the name that the person types', async () => {
    const cli = await openCli(scratchDir())
    await cli.waitForText('Name:')

    await cli.type('world')
    await cli.press('enter')

    const screen = await cli.waitForText('hello world')
    expect(screen).toContain('EXIT:0')
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
