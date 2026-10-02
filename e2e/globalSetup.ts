import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, renameSync, rmSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'

const venvs: Record<string, string[]> = {
  E2E_PYTHON_VENV: ['pytest==9.1.1', 'coverage==7.16.2'],
  E2E_PYTHON_VENV_WITHOUT_COVERAGE: ['pytest==9.1.1'],
}

export default function setup(): void {
  for (const [name, packages] of Object.entries(venvs)) {
    process.env[name] ??= venv(packages)
  }
}

// venv makes a venv with the packages one time in e2e/.venvs, so that a later run outside Docker needs no
// network.
function venv(packages: string[]): string {
  const folder = resolve(import.meta.dirname, '.venvs', packages.join('-'))
  if (existsSync(join(folder, 'bin', 'python'))) {
    return folder
  }
  mkdirSync(dirname(folder), { recursive: true })
  const temporary = `${folder}.${process.pid}`
  try {
    execFileSync('python3', ['-m', 'venv', temporary])
    execFileSync(join(temporary, 'bin', 'python'), ['-m', 'pip', 'install', '--quiet', ...packages])
    renameSync(temporary, folder)
  } finally {
    rmSync(temporary, { recursive: true, force: true })
  }
  return folder
}
