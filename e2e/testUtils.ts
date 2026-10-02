import { type ChildProcess, execFileSync, spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import type { Session } from 'tuistory'
import { onTestFinished } from 'vitest'

export function getExecutablePath(): string {
  const executablePath = process.env.EXECUTABLE
  if (!executablePath) {
    throw new Error('EXECUTABLE environment variable is required. Set it to the path of the mutants binary.')
  }
  return resolve(executablePath)
}

export function buildTag(): string {
  const tag = process.env.BUILD_TAG
  if (!tag) {
    throw new Error('BUILD_TAG environment variable is required. Set it to the tag the mutants binary was built with.')
  }
  return tag
}

export function scratchDir(): string {
  const dir = mkdtempSync(join(tmpdir(), 'mutants-e2e-'))
  onTestFinished(() => rmSync(dir, { recursive: true, force: true }))
  return dir
}

function withoutDeveloperSettings(env: Record<string, string> = {}): NodeJS.ProcessEnv {
  const inherited = { ...process.env }
  delete inherited.GOFLAGS
  delete inherited.GOWORK
  return { ...inherited, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1', ...env }
}

export interface Result {
  stdout: string
  stderr: string
  status: number | null
}

// startCli does not block, so a fake server in this process can still answer the
// requests the CLI makes, and a test can send a signal to the CLI while it runs.
export function startCli(
  cwd: string,
  args: string[] = [],
  env: Record<string, string> = {},
  executable = getExecutablePath(),
): { child: ChildProcess; result: Promise<Result> } {
  const child = spawn(executable, args, { cwd, env: withoutDeveloperSettings(env) })
  const result = new Promise<Result>((done, fail) => {
    let stdout = ''
    let stderr = ''
    child.stdout.on('data', (chunk) => (stdout += chunk))
    child.stderr.on('data', (chunk) => (stderr += chunk))
    child.on('error', fail)
    child.on('close', (status) => done({ stdout, stderr, status }))
  })
  return { child, result }
}

export function runCli(
  cwd: string,
  args: string[] = [],
  env: Record<string, string> = {},
  executable = getExecutablePath(),
): Promise<Result> {
  return startCli(cwd, args, env, executable).result
}

// openCli starts the CLI in a pseudo-terminal. When the CLI stops, the shell
// writes its exit status on the screen, so a test can wait for EXIT:0.
export async function openCli(cwd: string, args: string[] = [], env: Record<string, string> = {}): Promise<Session> {
  const { launchTerminal } = await import('tuistory')
  const assignments = Object.entries(env)
    .map(([name, value]) => `${name}='${value.replaceAll("'", `'\\''`)}'`)
    .join(' ')
  const session = await launchTerminal({
    command: 'sh',
    // The emulator starts in new line mode, where a line feed also goes back to
    // column 1. A real terminal does not, and a full screen program that moves
    // the cursor down with a line feed then draws in the wrong column, so
    // \e[20l turns the mode off before the CLI starts.
    args: ['-c', `printf '\\033[20l'; ${assignments} "${getExecutablePath()}" ${args.join(' ')}; echo "EXIT:$?"`],
    cwd,
    cols: 160,
    rows: 40,
  })
  onTestFinished(() => session.close())
  return session
}

export function git(dir: string, ...args: string[]): string {
  return execFileSync('git', args, { cwd: dir, encoding: 'utf8', env: withoutDeveloperSettings() }).trim()
}

export function writeFiles(dir: string, files: Record<string, string>): void {
  for (const [name, content] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, name)), { recursive: true })
    writeFileSync(join(dir, name), content)
  }
}

// goRepository makes a git repository with one commit, which holds a Go module and the files.
export function goRepository(files: Record<string, string> = {}): string {
  const dir = scratchDir()
  git(dir, 'init', '--quiet', '--initial-branch=main')
  git(dir, 'config', 'user.email', 'e2e@example.com')
  git(dir, 'config', 'user.name', 'e2e')
  git(dir, 'config', 'commit.gpgsign', 'false')
  writeFiles(dir, { 'go.mod': 'module example.com/fixture\n\ngo 1.22\n', ...files })
  git(dir, 'add', '--all')
  git(dir, 'commit', '--quiet', '--message', 'start')
  return dir
}

// pythonRepository makes a git repository with one commit, which holds a Python project and the files. Its
// .venv is a link to a venv of globalSetup.ts.
export function pythonRepository(files: Record<string, string> = {}, venv = pythonVenv()): string {
  const dir = scratchDir()
  git(dir, 'init', '--quiet', '--initial-branch=main')
  git(dir, 'config', 'user.email', 'e2e@example.com')
  git(dir, 'config', 'user.name', 'e2e')
  git(dir, 'config', 'commit.gpgsign', 'false')
  writeFiles(dir, { 'pyproject.toml': pyproject, '.gitignore': '.venv\n', ...files })
  git(dir, 'add', '--all')
  git(dir, 'commit', '--quiet', '--message', 'start')
  symlinkSync(venv, join(dir, '.venv'))
  return dir
}

export const pyproject = '[tool.pytest.ini_options]\npythonpath = ["."]\n'

// pythonVenv gives a venv with pytest and coverage.py, or with pytest only.
export function pythonVenv(coverage = true): string {
  const name = coverage ? 'E2E_PYTHON_VENV' : 'E2E_PYTHON_VENV_WITHOUT_COVERAGE'
  const venv = process.env[name]
  if (!venv) {
    throw new Error(`${name} environment variable is required. globalSetup.ts sets it.`)
  }
  return venv
}

export interface ReportedMutant {
  id: string
  file: string
  line: number
  column: number
  operator: string
  status: string
  original: string
  replacement: string
  bug?: string
  refs?: string[]
  detail?: string
}

// runMutants runs mutants run with --format json, and reads the mutants from the report.
export async function runMutants(dir: string, args: string[]): Promise<{ result: Result; mutants: ReportedMutant[] }> {
  const result = await runCli(dir, ['run', '--format', 'json', ...args])
  if (result.stdout.trim() === '') {
    throw new Error(`mutants run printed no report, and exited ${result.status}:\n${result.stderr}`)
  }
  return { result, mutants: JSON.parse(result.stdout).mutants }
}

// verdicts gives each mutant as its operator, its original text and its status.
export function verdicts(mutants: ReportedMutant[]): string[] {
  return mutants.map((m) => `${m.operator} ${m.original} ${m.status}`)
}
