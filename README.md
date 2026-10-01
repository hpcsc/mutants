# mutants

`mutants` finds weak tests in the lines that a branch changes. It makes one small change to the code at a
time, a mutant, and runs the tests after each one. When the tests still pass, the mutant lives, and that
shows a behaviour that no test pins. [docs/design.md](docs/design.md) tells how it works.

## Use

`mutants` needs git 2.30 or later, the Go toolchain, and [ast-grep](https://ast-grep.github.io) 0.45.0 or
later on the `PATH`. Run it in a git repository that holds a Go module.

```shell
mutants run                          # the mutants of the lines that the branch changes
mutants run --base HEAD              # only the lines that are not committed
mutants run --all ./internal/order   # every line of one package; ./internal/... adds the subfolders
mutants rerun 'internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1'
mutants operators                    # the operators and their rules
```

`mutants run` compares the work tree with the merge base of `HEAD` and `--base`, which is `origin/HEAD`
when you do not set it. It counts the lines that are not committed, and every line of an untracked file.
It never writes to the work tree or to the index.

Each row shows one mutant that needs a look, and ends with the id that `mutants rerun` takes:

```text
LIVED:
  internal/order/handler.go:42 BRANCH_IF: { return nil, fmt.Errorf("load the accounts ... -> {}  [internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1]
NOT COVERED:
  internal/order/handler.go:43 RETURN_ERROR_NIL: fmt.Errorf("load the accounts ... -> nil  [internal/order/handler.go:(*Handler).accounts:RETURN_ERROR_NIL#1]
mutants: 81, killed: 64, lived: 1, not covered: 3, not viable: 13 (base 1a2b3c4d5e)
```

| Status | Meaning | Counts as a survivor |
| --- | --- | --- |
| KILLED | a test failed | no |
| LIVED | every test passed | yes |
| NOT COVERED | no test runs the line | yes |
| NOT VIABLE | the mutant does not build | no |
| TIMED OUT | the tests ran past the limit | no |
| INFRA ERROR | the host stopped the run, for example out of memory | no |

| Command | Exit codes |
| --- | --- |
| `mutants run` | 0 no survivor, 10 survivors, 124 the `--limit`, 2 a usage or tool error, 130 an interrupt |
| `mutants rerun ID` | 0 killed, 10 lived or not covered, 1 no verdict, 2 an unknown id |

The flags of `mutants run`:

| Flag | Does |
| --- | --- |
| `--base REF` | compares with the merge base of `HEAD` and `REF` |
| `--all FOLDER...` | runs every line of the files in each folder |
| `--workers N` | tests `N` mutants at once, 4 by default |
| `--limit DURATION` | stops the whole run, writes the mutants that got a verdict, and exits 124 |
| `--build-limit DURATION` | stops the build of one mutant, 2 minutes by default |
| `--tags a,b` | gives the build tags to `go list`, to the coverage run and to each build |
| `--operators=-ERRORF_WRAP,+NAME` | `-NAME` takes an operator out, `+NAME` adds one, and `NAME` runs only the named operators |
| `--format rows\|json` | prints rows, or one JSON document |
| `--json PATH`, `--stryker PATH` | also writes the JSON report, or the Stryker report for its HTML viewer |

A repository can keep its settings in `.mutants.yml` at its root. A flag wins over the file.

```yaml
base: origin/main
workers: 4
tags: [unit]
operators: [-SWAP_FIELDS]
exclude: ["**/*_gen.go", "vendor/**"]
```

A repository adds its own operators as ast-grep rules with a `fix`, in `.mutants/operators/go/`. A rule
with the id of a standard rule replaces that rule. `mutants operators` lists each rule and its file.

## Install

The installer downloads the latest release for your platform, verifies its checksum, and puts the
binary where you can start using it. It needs `sh`, `curl`, `jq`, `tar`, and `gzip`.

```shell
sh <(curl -fsSL https://raw.githubusercontent.com/hpcsc/mutants/main/scripts/install.sh)
```

It asks for the release channel and the install directory. To skip the questions on the command line:

```shell
sh <(curl ...) --channel release --dir ~/.local/bin
```

The channel is `release` (the latest stable release) or `prerelease` (the latest build of `main`).
Without `--dir`, the binary lands in `~/.local/bin` (or `$INSTALL_DIR`). When more than one version is
available in the channel, the installer lists them for you to pick.

`GITHUB_TOKEN` or `GH_TOKEN` gives access to a private repository.

`mutants update` also installs from a release, and it replaces this very binary, so the
installer and the update command do the same kind of job; use whichever you find convenient on a fresh
machine.

## Build

```shell
task build            # build ./bin/mutants
task run -- --help    # run the CLI from the source
```

## Version and update

```shell
mutants version                # the tag of a release or a prerelease, or the commit of any other build
mutants update                 # install the latest release
mutants update --prerelease    # install the latest prerelease, a build of main
mutants update --check         # only tell you whether this build is the latest
```

`internal/version` reports the version. A release build gets its tag from goreleaser, through
`-ldflags -X .../internal/version.releaseTag`. Any other build reports the short commit sha that Go
keeps in the build information, with `-dirty` after it when the working tree had changes.

`mutants update` downloads the archive for your platform from a GitHub release, checks it
against the `checksums.txt` of that release, and then replaces the binary. It names each step on
stderr, and in a terminal it shows how much of the download has arrived. `GITHUB_TOKEN` or `GH_TOKEN`
gives access to a private repository.

Releases and prereleases are two channels. Each command installs the latest build of its channel when
this build is a different one, so `mutants update` on a prerelease goes back to the latest
release. A build from a commit is not a release or a prerelease, so `mutants update` does not
replace it unless you add `--force`.

## Goreleaser

- Run goreleaser in local: `task release:local`. This will generate a snapshot build under `./dist`
- Create a release:

```shell
git tag vX.X.X
git push origin vX.X.X
```

This will trigger the release workflow, which runs the CI checks and then creates a Github Release with
binaries for MacOS and Linux.

Each push to `main` starts the prerelease workflow. It tags the commit with the next patch after the
latest release, the run number and the commit, for example `v0.2.1-42.g4829f92`, publishes that tag as a
prerelease, and then keeps only the 5 newest prereleases.

`On Demand Build` builds a snapshot of any ref from Github Actions and uploads the archives as artifacts.

## E2E Test

The end-to-end tests run the built binary against small Go modules in git repositories that the tests
make, with known answers. They are in `e2e/`, and [docs/e2e-tests.md](docs/e2e-tests.md) tells how they
work.

```shell
task test:e2e          # in Docker, as CI does
task test:e2e:local    # on this machine, needs node, Go and ast-grep
```
