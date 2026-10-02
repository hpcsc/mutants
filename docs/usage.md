# Commands, Flags and Settings of mutants

This page lists each command of `mutants`, its flags and its exit codes, the formats of its reports, and the
settings that a repository keeps in `.mutants.yml`. The [README](../README.md) shows a first run.

## The lines that a run tests

`mutants run` makes mutants only on the changed lines:

- It compares the work tree with the merge base of `HEAD` and the base. The merge base is the point where
  the branch left the base, so commits that land on the base later do not count.
- The base is `origin/HEAD`, unless `--base` or `.mutants.yml` sets another one.
- It includes the lines that you did not commit, and each line of an untracked file.
- `mutants run --base HEAD` tests only the changes that you did not commit.
- `mutants run --all FOLDER...` tests each line of the files in each folder, tracked or untracked.
  `FOLDER/...` adds the subfolders.

A mutant runs when its change touches a changed line. `mutants` never writes to the work tree or to the git
index. When the changed lines give no mutant, it says so in one line:

```text
no mutant: 0 changed lines in 0 files (base 1a2b3c4d5e)
```

## Commands

| Command | Does |
| --- | --- |
| `mutants run` | runs the mutants of the changed lines |
| `mutants run --all FOLDER...` | runs the mutants of each line in the folders |
| `mutants rerun ID` | runs one mutant again, by its id, with no diff and no cache |
| `mutants operators` | lists each operator, whether it runs by default, and its rules |
| `mutants version` | prints the version, see [docs/install.md](install.md) |
| `mutants update` | installs the latest release, see [docs/install.md](install.md) |

## Flags of mutants run

| Flag | Does |
| --- | --- |
| `--base REF` | compares with the merge base of `HEAD` and `REF`. The default is `origin/HEAD`. |
| `--all FOLDER...` | tests each line of the files in each folder |
| `--workers N` | tests `N` mutants at the same time. The default is 4. |
| `--limit DURATION` | stops the whole run after this time, prints the mutants that have a status, and exits with 124 |
| `--build-limit DURATION` | the least time for the build of one mutant. The default is 2 minutes. See [Time limits](#time-limits). |
| `--tags a,b` | the build tags for `go list`, for the coverage run and for each build |
| `--operators=-NAME,+NAME` | `-NAME` takes an operator out, `+NAME` adds one, and `NAME` with no sign runs only the named operators |
| `--format rows\|json` | prints rows, or one JSON document. The default is rows. |
| `--json PATH` | also writes the JSON report to `PATH` |
| `--stryker PATH` | also writes the Stryker report to `PATH` |

## Flags of mutants rerun

`mutants rerun` takes the id in `[ ]` at the end of a row:

```shell
mutants rerun 'internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1'
```

It finds the mutant with all the rules of its operator, also an operator that is off by default. It needs
no diff, so it also works after a commit. It prints one row, and the reason for the status below the row.

| Flag | Does |
| --- | --- |
| `--tags a,b` | the build tags for `go list`, for the coverage run and for the build |
| `--build-limit DURATION` | the least time for the build of the mutant |

## Exit codes

| Command | Code | Meaning |
| --- | --- | --- |
| `mutants run` | 0 | no mutant survived |
| | 10 | at least one mutant survived |
| | 124 | the run reached `--limit` |
| | 2 | a usage error or a tool error, for example a test that fails with the real code |
| | 130 | an interrupt or SIGTERM stopped the run |
| `mutants rerun` | 0 | a test killed the mutant |
| | 10 | the mutant lived, or no test runs its line |
| | 1 | no verdict: the mutant timed out, did not build, or the computer stopped it |
| | 2 | no mutant has this id |

## Statuses

| Status | Meaning | Survivor |
| --- | --- | --- |
| KILLED | a test failed | no |
| LIVED | every test passed | yes |
| NOT COVERED | no test runs the line | yes |
| NOT VIABLE | the mutant does not build | no |
| TIMED OUT | the tests ran past their time limit two times | no |
| INFRA ERROR | the computer stopped the run, for example when it had no more memory, or the build ran past its limit | no |

A package whose tests fail with the real code stops the run with exit code 2, because its mutants cannot get
a verdict.

## Rows

The rows are the default output. They show only the mutants that need a look, grouped by status:

```text
LIVED:
  internal/order/handler.go:42 BRANCH_IF: { return nil, fmt.Errorf("load the accounts ... -> {}  [internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1]
NOT COVERED:
  package cmd/report has no test files: 12 mutants
  internal/order/handler.go:43 RETURN_ERROR_NIL: fmt.Errorf("load the accounts ... -> nil  [internal/order/handler.go:(*Handler).accounts:RETURN_ERROR_NIL#1]
mutants: 81, killed: 64, lived: 1, not covered: 15, not viable: 1 (base 1a2b3c4d5e)
```

- Each row gives the file and the line, the operator, the code before and after the change, and the id.
- A row cuts the code at 40 characters. When the code is long, the row starts a few words before the
  first difference, so that you see the difference.
- An empty replacement shows as `(nothing)`.
- A package with no test files gives one row for all its mutants.
- The last line counts the mutants of each status, and gives the base.

The progress lines and the messages go to stderr, so they do not mix with a report on stdout.

## Mutant ids

An id has this shape:

```text
<file from the repository root>:<function>:<operator>#<n>
internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1
```

`n` counts the mutants of that operator in that function, in the order of the code. So a change in another
function does not change the id, and the id is the same with `--base HEAD` and with the base of the branch.

## The JSON report

`--format json` prints one JSON document, and `--json PATH` writes the same document to a file. It holds
each mutant, also the killed ones:

```json
{
  "base": "4eea6345364a533490b97b7ed4d7b188d1c99e5c",
  "mutants": [
    {
      "id": "shop/discount.go:Discount:CONDITIONALS_BOUNDARY#1",
      "file": "shop/discount.go",
      "line": 5,
      "column": 5,
      "operator": "CONDITIONALS_BOUNDARY",
      "status": "KILLED",
      "original": "total >= 100",
      "replacement": "total > 100",
      "detail": "--- FAIL: TestDiscount (0.00s)"
    }
  ]
}
```

`detail` gives the reason for the status, such as the test that failed or the build error. It is not there
when there is no reason. The JSON encoder writes `<`, `>` and `&` in strings as `<`, `>` and
`&`, and a JSON parser reads them back as the same characters.

## The Stryker report

`--stryker PATH` writes version 2 of the
[mutation-testing-elements](https://github.com/stryker-mutator/mutation-testing-elements) report format.
Its HTML viewer shows each mutant in the code of its file.

## Settings in .mutants.yml

A repository can keep its settings in `.mutants.yml` at its root. A flag wins over the file.

```yaml
base: origin/main
workers: 4
tags: [unit]
operators: [-ERRORF_WRAP]
exclude: ["**/*_gen.go", "vendor/**"]
zero_functions: [maybe.None, caseautoresolve.Submitted]
```

| Key | Does |
| --- | --- |
| `base` | the base of the changed lines, as `--base` |
| `workers` | the number of mutants that run at the same time, as `--workers` |
| `tags` | the build tags, as `--tags` |
| `operators` | the operators, as `--operators` |
| `exclude` | the files that get no mutant, as globs from the repository root |
| `zero_functions` | the functions that return the zero value of their type, as `package.Function`. `FIELD_ZERO` skips a field whose value is a call of one of them, because the removal of that field changes nothing. Use the name of the package, not its path. |

An unknown key is an error that names the key, its line and the known keys.

## Your own operators

A repository can add its own operators as [ast-grep](https://ast-grep.github.io) rules with a `fix`. Put
each rule in a YAML file in `.mutants/operators/go/`:

```yaml
id: NIL_MAP
language: go
rule:
  pattern: map[$K]$V{}
fix: nil
```

- The operator is the part of the id before the first `/`. So the rules `NIL_MAP/empty` and `NIL_MAP/make`
  make one operator, `NIL_MAP`.
- A rule with the id of a standard rule replaces that rule.
- A rule with `metadata: {default: off}` runs only when `--operators` or `.mutants.yml` names its operator.
- `mutants operators` lists each rule, and the file of each rule that the repository adds.

## Time limits

| Limit | Value |
| --- | --- |
| The build of one mutant | 3 × the time of the build of the real code of its package, and at least `--build-limit` |
| The tests of one mutant | 3 × the time of the tests of its package with the real code, plus 5 s |
| The whole run | `--limit`, with no limit by default |

The load of the computer can grow during a run. So a mutant whose tests run past their limit runs one more
time with twice the limit. It is TIMED OUT only when the second run also runs past the limit.

## Workers

| Setting | Default | Notes |
| --- | --- | --- |
| `--workers` | 4 | each worker tests one mutant at a time |
| `-p` in `GOFLAGS` | 2 | for each build, unless you set `GOFLAGS` |
| `GOMAXPROCS` of the tests | the number of CPUs ÷ the number of workers | so the tests of all the workers do not use all the CPUs at the same time |
