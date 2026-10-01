import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmodSync, copyFileSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { join } from 'node:path'
import { describe, expect, it, onTestFinished } from 'vitest'
import { buildTag, getExecutablePath, runCli, scratchDir } from '../testUtils'

function platform(): string {
  const arch = process.arch === 'x64' ? 'amd64' : process.arch
  return `${process.platform}-${arch}`
}

interface FakeRelease {
  tag: string
  prerelease: boolean
  // newBinary is the mutants in the archive for this platform.
  newBinary: string
}

// fakeReleases serves a GitHub API with these releases, published in the order
// of the list.
async function fakeReleases(releases: FakeRelease[]): Promise<string> {
  const assets = new Map<string, Record<string, Buffer>>()
  for (const release of releases) {
    const dir = scratchDir()
    writeFileSync(join(dir, 'mutants'), release.newBinary, { mode: 0o755 })
    const archive = `mutants-${platform()}.tar.gz`
    execFileSync('tar', ['-czf', join(dir, archive), '-C', dir, 'mutants'])
    const data = readFileSync(join(dir, archive))
    assets.set(release.tag, {
      [archive]: data,
      'checksums.txt': Buffer.from(`${createHash('sha256').update(data).digest('hex')}  ${archive}\n`),
    })
  }

  const server = createServer((request, response) => {
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
    const describe = (release: FakeRelease, index: number) => ({
      tag_name: release.tag,
      prerelease: release.prerelease,
      published_at: new Date(Date.UTC(2026, 8, 1 + index)).toISOString(),
      assets: Object.keys(assets.get(release.tag) ?? {}).map((name) => ({ name, url: `${base}/assets/${release.tag}/${name}` })),
    })
    const path = new URL(request.url ?? '/', base).pathname
    if (path === '/repos/hpcsc/mutants/releases/latest') {
      let latest = -1
      releases.forEach((release, index) => {
        if (!release.prerelease) latest = index
      })
      response.setHeader('content-type', 'application/json')
      response.end(JSON.stringify(describe(releases[latest], latest)))
      return
    }
    if (path === '/repos/hpcsc/mutants/releases') {
      response.setHeader('content-type', 'application/json')
      response.end(JSON.stringify(releases.map(describe).reverse()))
      return
    }
    const [, , tag, name] = path.split('/')
    const data = assets.get(tag)?.[name]
    if (request.headers.accept === 'application/octet-stream' && data) {
      response.end(data)
      return
    }
    response.statusCode = 404
    response.end()
  })
  await new Promise<void>((done) => server.listen(0, '127.0.0.1', done))
  onTestFinished(() => new Promise<void>((done) => server.close(() => done())))
  return `http://127.0.0.1:${(server.address() as AddressInfo).port}`
}

// fakeRelease serves a GitHub API whose only release is tag.
function fakeRelease(tag: string, newBinary: string): Promise<string> {
  return fakeReleases([{ tag, prerelease: false, newBinary }])
}

function againstApi(api: string): Record<string, string> {
  return { GITHUB_API_URL: api, GITHUB_TOKEN: '', GH_TOKEN: '' }
}

function copyOfCli(): string {
  const copy = join(scratchDir(), 'mutants')
  copyFileSync(getExecutablePath(), copy)
  chmodSync(copy, 0o755)
  return copy
}

describe('mutants update', () => {
  it('--check reports a newer release and changes nothing', async () => {
    const api = await fakeRelease('v9.0.0', '#!/bin/sh\necho "the new mutants"\n')

    const result = await runCli(scratchDir(), ['update', '--check'], againstApi(api))

    expect(result.status).toBe(0)
    expect(result.stdout).toContain(`mutants v9.0.0 is available. This is ${buildTag()}.`)
  })

  it('replaces the binary with the newer release', async () => {
    const api = await fakeRelease('v9.0.0', '#!/bin/sh\necho "the new mutants"\n')
    const copy = copyOfCli()

    const update = await runCli(scratchDir(), ['update'], againstApi(api), copy)

    expect(update.status).toBe(0)
    expect(update.stdout).toContain(`Updated mutants from ${buildTag()} to v9.0.0`)
    const replaced = await runCli(scratchDir(), ['version'], {}, copy)
    expect(replaced.stdout).toContain('the new mutants')
  })

  it('names each step on stderr while it updates, so that it does not look stuck', async () => {
    const api = await fakeRelease('v9.0.0', '#!/bin/sh\necho "the new mutants"\n')
    const copy = copyOfCli()

    const update = await runCli(scratchDir(), ['update'], againstApi(api), copy)

    expect(update.status).toBe(0)
    expect(update.stderr).toBe(
      `Finding the latest release of hpcsc/mutants…\nDownloading mutants v9.0.0 for ${platform()}…\n`,
    )
  })

  it('--prerelease replaces the binary with the latest prerelease', async () => {
    const api = await fakeReleases([
      { tag: 'v9.0.0', prerelease: false, newBinary: '#!/bin/sh\necho "the release"\n' },
      { tag: 'v9.0.1-7.gabc1234', prerelease: true, newBinary: '#!/bin/sh\necho "the prerelease"\n' },
    ])
    const copy = copyOfCli()

    const update = await runCli(scratchDir(), ['update', '--prerelease'], againstApi(api), copy)

    expect(update.status).toBe(0)
    expect(update.stdout).toContain(`Updated mutants from ${buildTag()} to v9.0.1-7.gabc1234`)
    const replaced = await runCli(scratchDir(), ['version'], {}, copy)
    expect(replaced.stdout).toContain('the prerelease')
  })

  it('without --prerelease takes the latest release, also when a newer prerelease exists', async () => {
    const api = await fakeReleases([
      { tag: 'v9.0.0', prerelease: false, newBinary: '#!/bin/sh\necho "the release"\n' },
      { tag: 'v9.0.1-7.gabc1234', prerelease: true, newBinary: '#!/bin/sh\necho "the prerelease"\n' },
    ])
    const copy = copyOfCli()

    const update = await runCli(scratchDir(), ['update'], againstApi(api), copy)

    expect(update.status).toBe(0)
    expect(update.stdout).toContain(`Updated mutants from ${buildTag()} to v9.0.0`)
    const replaced = await runCli(scratchDir(), ['version'], {}, copy)
    expect(replaced.stdout).toContain('the release')
  })

  it('says so when the binary is the latest release', async () => {
    const api = await fakeRelease(buildTag(), '#!/bin/sh\necho "never installed"\n')

    const result = await runCli(scratchDir(), ['update'], againstApi(api))

    expect(result.status).toBe(0)
    expect(result.stdout).toContain(`mutants ${buildTag()} is the latest release.`)
  })
})
