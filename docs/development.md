# Build, Test and Release mutants

This page is for people who change the code of `mutants`. [docs/design.md](design.md) tells how the code
works, and [docs/language-adapters.md](language-adapters.md) tells how to add a language.

## Tools

[mise](https://mise.jdx.dev) installs the tools that `mise.toml` pins: ast-grep, node, task, gotestsum,
goreleaser, shellcheck and govulncheck. Go comes from `go.mod`.

```shell
mise install
```

## Build and run

```shell
task build            # builds ./bin/mutants
task run -- --help    # runs the CLI from the source, with the arguments after --
```

## Tests

| Command | Runs |
| --- | --- |
| `task test:unit` | the Go tests, with the build tag `unit`, the race detector and coverage |
| `task test:e2e` | the end-to-end tests in Docker, against a new binary, as CI runs them |
| `task test:e2e:local` | the end-to-end tests on this machine. They need node, Go and ast-grep. |
| `task test:fuzz` | the fuzz tests of the readers of mutant ids, proposals, `.mutants.yml` and git diffs, for 30 s each. `task test:fuzz FUZZ_TIME=5m` changes the time. `task test:unit` runs only their seeds. |
| `task test:regression` | `mutants` on the newest commits of open-source repositories, with a check of each run. [docs/regression-test.md](regression-test.md) tells how it works. |
| `task test:shellcheck` | shellcheck on the shell scripts and the git hooks |
| `task test:vulnerabilities` | govulncheck on the Go code |

Each Go test file starts with `//go:build unit`, so `go test` needs `-tags unit` to find the tests. The
end-to-end tests run the binary against small Go modules in git repositories that the tests make.
[docs/e2e-tests.md](e2e-tests.md) tells how they work.

CI runs shellcheck, the build, the unit tests, govulncheck and the end-to-end tests on each push to a
branch other than `main`. For `main`, the Prerelease workflow runs the same checks.

The Mutants workflow runs `mutants` on its own changes after each push: against `origin/main` for a branch,
and against the commit before the push for `main`. `.mutants.yml` gives it the build tag `unit`. The job
summary shows the rows, and the artifact `mutants-reports` holds the JSON and the Stryker report. A survivor
does not fail the job, and the run stops after 60 minutes. The release does not wait for this workflow.

## How a build gets its version

`internal/version` gives the version that `mutants version` prints:

- A release build gets its tag from goreleaser, through
  `-ldflags -X github.com/hpcsc/mutants/internal/version.releaseTag=<tag>`.
- Any other build reports the short commit that Go keeps in the build information. It adds `-dirty` when
  the work tree had changes that you did not commit.

## Releases

| Workflow | Starts on | Does |
| --- | --- | --- |
| Release | a push of a tag `vX.Y.Z` | runs the CI checks, and then makes a GitHub release with the binaries for macOS and Linux |
| Prerelease | a push to `main` | runs the CI checks, tags the commit, publishes the tag as a prerelease, and keeps only the 5 newest prereleases |
| On Demand Build | a manual start in GitHub Actions | builds a snapshot of any ref, and uploads the archives as artifacts |

To make a release:

```shell
git tag vX.Y.Z
git push origin vX.Y.Z
```

The tag of a prerelease is the next patch after the latest release, the run number and the commit, for
example `v0.2.1-42.g4829f92`.

To build a snapshot of the release on this machine, in `./dist`:

```shell
task release:local
```
