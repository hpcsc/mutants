# Install, Update and Check the Version of mutants

## What mutants needs

| Tool | Version | Why |
| --- | --- | --- |
| git | 2.30 or later | `mutants` reads the changed lines from git |
| Go | the version that your module needs | `mutants` builds your tests and runs them |
| [ast-grep](https://ast-grep.github.io/guide/quick-start.html) | 0.45.0 or later | `mutants` finds the code to change with ast-grep rules |

Each tool must be on the `PATH`. `mutants` stops with an error when the `PATH` has no ast-grep, or an older one.

## Install with the script

```shell
sh <(curl -fsSL https://raw.githubusercontent.com/hpcsc/mutants/main/scripts/install.sh)
```

The script downloads a release of `mutants` for your platform from GitHub. It checks the download against the
checksums of the release, and then puts the binary in the install folder. It needs `curl`, `jq`, `tar`,
`gzip`, and `sha256sum` or `shasum`.

The script asks these questions:

1. The release channel: `release` for the stable releases, or `prerelease` for the builds of `main`.
2. The version, when the channel has more than one.
3. The install folder.

To give the channel and the folder on the command line:

```shell
sh <(curl -fsSL https://raw.githubusercontent.com/hpcsc/mutants/main/scripts/install.sh) --channel release --dir ~/.local/bin
```

| Option or variable | Does |
| --- | --- |
| `--channel release` or `--channel prerelease` | sets the release channel |
| `--dir PATH` | sets the install folder |
| `INSTALL_DIR` | the default install folder. Without it, the default is `~/.local/bin`. |
| `GITHUB_TOKEN` or `GH_TOKEN` | a token that gives access to a private repository |

## Update

`mutants update` replaces the binary that runs it with the latest build of its channel:

```shell
mutants update                 # install the latest release
mutants update --prerelease    # install the latest prerelease, a build of main
mutants update --check         # only tell whether this build is the latest
```

- It downloads the archive for your platform, checks it against the `checksums.txt` of the release, and then
  replaces the binary. It names each step on stderr, and in a terminal it shows the progress of the download.
- Each command installs the latest build of its channel when this build is a different one. So
  `mutants update` on a prerelease goes back to the latest release.
- A build from a commit is not a release or a prerelease, so `mutants update` does not replace it. Add
  `--force` to replace it.
- `GITHUB_TOKEN` or `GH_TOKEN` gives access to a private repository.

## Check the version

```shell
mutants version
```

A release or a prerelease prints its tag, for example `v0.2.1` or `v0.2.1-42.g4829f92`. Any other build
prints the short commit that it came from. `-dirty` after the commit tells that the work tree had changes
that you did not commit.

## Build from the source

[docs/development.md](development.md) tells how to build `mutants` from its source.
