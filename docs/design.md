# Design

`mutants` finds weak tests in the lines that a branch changes. It makes one small change to the code at a
time, a mutant, and runs the tests after each one. When the tests still pass, the mutant lives, and that
shows a behaviour that no test pins.

Go is the first language. ast-grep finds the code to change, and `mutants` runs the tests and reports the
result. The design keeps the language out of the core, so that a second language needs a rule pack and a
language adapter, and no change to the core.

## What it must do

Each requirement comes from a fault that a measurement of five Go mutation tools found on a large Go
monorepo (1,652 packages): gremlins, mutago, gomutants, mutest and togi.

| Requirement | The fault that it prevents |
| --- | --- |
| Mutate only the lines that the branch changes. | On whole packages, 138 of 143 survivors were in code that the branch did not change. |
| Read the diff with fixed prefixes. | With `diff.mnemonicPrefix` set, git prints `+++ w/<path>`. gomutants, mutago and togi then found no changed line, and each reported a clean result with exit 0. |
| Count uncommitted and untracked lines. | The build step of a flow runs before the commit. togi reads only `<base>..HEAD`, and no tool reads untracked files. |
| Never write into the work tree or the index. | gomutants, mutago and togi left report or lock files in the project root. mewt and universalmutator write each mutant into the real file. |
| Find each package with `go list`. | gremlins matches the folder name to the package name. Where they differ, it ran the wrong package and reported every mutant as killed. That was 31 % of the packages. |
| Keep a test failure, a build failure, a timeout and a host failure apart. | mutago and gremlins count every exit code 1 as a kill, and both reported false kills. |
| Run each mutant fresh. | mutago lets Go's test cache answer, and its first and second runs gave different verdicts. |
| Stop the whole process group at the limit, and set the limit from a baseline. | gremlins left a test process alive. togi waits a fixed 30 s, so a small module with hangs took 132 s, against 42 s with a limit from a baseline. |
| Test every mutant by default. | togi tests 20 mutants by default, and it took a sample of 20 from 107. |
| Make the mutants that no tool makes. | No measured tool swaps two values of the same type, or stops a loop after its first item. Both shapes hid real test gaps. |
| Run one mutant again, with clear exit codes. | An agent or a reviewer checks one survivor at a time, after a new test. |
| Give the same verdicts in two runs. | gomutants did, in 4 of 4 pairs. mutago did not, in 0 of 3. |

## Where it runs

`mutants` is a command. An implementation flow calls it at two points, and a reviewer calls it to check one
survivor.

```mermaid
flowchart TD
    subgraph BUILD["build step, once for each task"]
        direction LR
        T1["tests pass"] --> M1["mutants run --base HEAD"]
        M1 -- "LIVED or NOT COVERED rows" --> F1["add a test, or say why none"]
        F1 --> R1["mutants rerun ID"]
        R1 --> C1["commit"]
    end
    subgraph AUDIT["audit, once for each round"]
        direction LR
        M2["mutants run --base MERGE_BASE --format json"] --> L["test lens"]
        L -- "finding with a mutant id" --> RF["refuter"]
        RF --> R2["mutants rerun ID"]
    end
    C1 --> M2
```

- **In the build step**, `--base HEAD` covers the uncommitted lines of the open task.
- **In the audit**, `--base` is the merge base of the branch, so the run covers the whole branch.
- **`rerun` exits 10 when the mutant lives** and 0 when a test kills it. So the refuter gets a verdict from the
  exit code, and needs no judgement.

## How a run works

```mermaid
flowchart LR
    subgraph FIND["1. find the mutants"]
        direction TB
        A["changed lines<br/>git diff --merge-base, untracked files"] --> C["ast-grep scan<br/>all rules, all changed files"]
        C --> F["filters<br/>Go: go/types"]
        F --> G["ids"]
        G --> E["keep the mutants on changed lines"]
    end
    subgraph RUN["2. run them"]
        direction TB
        H["coverage of each package<br/>NOT COVERED without a test run"] --> I["workers"]
        I --> J["runner<br/>Go: overlay build, then the test binary"]
        J --> K["status of each mutant"]
    end
    subgraph OUT["3. report"]
        direction TB
        L["rows, JSON, Stryker"] --> X["exit code"]
    end
    E --> H
    K --> L
    class A,E,G,I,K,L,X core
    class C astg
    class F,H,J lang
    classDef core stroke:#4a6fd1,stroke-width:3px
    classDef astg stroke:#3f9a55,stroke-width:3px
    classDef lang stroke:#d07a2d,stroke-width:3px
```

A blue border marks a step of the core. A green border marks ast-grep. An orange border marks a step that comes from the adapter of the language.

1. **Changed lines.** `mutants` asks git for the changed lines (see [Changed lines](#changed-lines)).
2. **Candidates.** One ast-grep call scans the changed files with every rule. Each match is a candidate edit,
   with byte offsets and the replacement text.
3. **Filters.** The language adapter drops a candidate that cannot build or that changes nothing (see
   [Filters](#filters)).
4. **Ids.** Each candidate in the changed files gets an id that stays the same when other code moves (see
   [Mutant ids](#mutant-ids)).
5. **Scope.** It keeps a mutant only when its edit touches a changed line.
6. **Coverage.** One coverage run for each package marks the mutants that no test runs. Those mutants are NOT
   COVERED, and they do not run.
7. **Run.** The workers give each mutant to the runner of its language. The runner returns a status.
8. **Report.** It prints the rows, writes the files that the user asks for, and sets the exit code.

### Changed lines

```sh
git -c core.quotePath=false diff --merge-base <base> --unified=0 --inter-hunk-context=0 \
    --no-color --no-ext-diff --no-textconv --no-relative --find-renames \
    --src-prefix=a/ --dst-prefix=b/ -- ':(glob)**/*.go' ':(glob,exclude)<exclude>'...
git ls-files --others --exclude-standard -z -- ':(glob)**/*.go' ':(glob,exclude)<exclude>'...
```

- **`--src-prefix` and `--dst-prefix` fix the prefixes**, so `diff.mnemonicPrefix` and `diff.noprefix` in a
  user config change nothing. The other flags fix the context, the colour, the diff tool, the text
  conversion, `diff.relative` and the rename detection in the same way.
- **`--merge-base`** compares the work tree with the point where the branch left `<base>`. So it reads
  uncommitted lines, and it ignores commits that landed on `<base>` later.
- **Every line of an untracked file counts as changed.** `mutants` reads the list of untracked files. It does
  not add them to the index.
- **The default base** is the merge base of `HEAD` and `origin/HEAD`. `--base` sets it.
- **A name that git quotes** keeps its name: `mutants` reads the C quotes of git, and the tab that git puts
  after a name with a space.
- **`--all FOLDER...`** reads every line of the files in each folder, tracked or untracked, from
  `git ls-files`. `FOLDER/...` adds the subfolders.

When the changed files give no mutant, `mutants` says so in one line. That line shows a diff that is empty by
mistake:

```text
no mutant: 0 changed lines in 0 files (base 1a2b3c4d5e)
```

### Operators

An operator is one kind of change, for example "replace `<` with `<=`". A rule is one ast-grep rule with a
`fix`. An operator has one or more rules, or a hook in Go when a rule cannot express it.

```yaml
# internal/operator/operators/go/CONDITIONALS_BOUNDARY.yml, the first two of its four rules
id: CONDITIONALS_BOUNDARY/lt
language: go
rule:
  pattern: $A < $B
fix: $A <= $B
---
id: CONDITIONALS_BOUNDARY/gt
language: go
rule:
  pattern: $A > $B
fix: $A >= $B
```

```yaml
# internal/operator/operators/go/BREAK_AT_END.yml
id: BREAK_AT_END
language: go
rule:
  pattern: for $$$HEAD { $$$BODY }
  not:
    has:
      field: body
      has:
        kind: statement_list
        has:
          any:
            - kind: return_statement
            - kind: break_statement
            - kind: continue_statement
          nthChild:
            position: 1
            reverse: true
fix: |-
  for $$$HEAD {
  	$$$BODY
  	break
  }
```

The `not` part skips a loop whose last statement is `return`, `break` or `continue`, because a `break` after
it changes nothing. `ast-grep scan --json` returns each match with its `replacement` and its byte offsets, so
one match is one mutant. `mutants` runs the scan with `--hint`, so a rule with `severity: error` does not
change the exit code of ast-grep. The rules live in three places, and a later place adds to an earlier one:

| Place | Holds |
| --- | --- |
| `internal/operator/operators/<language>/` in the binary (`go:embed`) | the standard pack |
| `.mutants/operators/<language>/` in the target repository | rules for that repository, for example a shape that its code often gets wrong. A rule with the id of a standard rule replaces that rule. |
| `--operators` | the operators to run in this call: `-NAME` takes an operator out, `+NAME` adds one, and `NAME` with no sign runs only the named operators |

A rule with `metadata: {default: off}` runs only when `--operators` names its operator. `mutants operators`
lists each operator, whether it runs by default, and its rules.

A rule can also carry a filter, so most noise filters are rules too. This rule skips each `+` inside a
zerolog call chain that ends in `Msg`, `Msgf` or `Send`. `\s*` finds a chain over more than one line, where
white space comes between the dot and `Msg`:

```yaml
id: ARITHMETIC_BASE/plus
language: go
rule:
  pattern: $A + $B
  not:
    inside:
      stopBy: end
      kind: call_expression
      has:
        field: function
        regex: \.\s*(Msgf?|Send)$
fix: $A - $B
```

A hook is Go code for an operator that needs more than one match. `SWAP_FIELDS` is a hook: a rule can swap
the two fields of a literal with exactly two fields, but not each adjacent pair of a longer literal.

```go
package operator

// Hook makes the edits of an operator from the matches of its rules in one file.
type Hook interface {
	Operator() string
	Edits(source []byte, matches []Match) []Edit
}
```

A rule cannot know a type, so a rule gives every candidate and the type filter of the adapter chooses.
`RETURN_ZERO` has one rule for each zero value (`nil`, `0`, `""` and `false`) and one rule that makes a struct
literal `T{}`, and the filter keeps the one that is the zero value of the slot. `ARGUMENT_ZERO` does the same
for the parameter of a call argument.

The Go pack for v1:

| Operator | Change | Notes |
| --- | --- | --- |
| `CONDITIONALS_BOUNDARY` | `<` to `<=`, `>` to `>=`, and back | |
| `CONDITIONALS_NEGATION` | `==` to `!=`, `<` to `>=`, and the rest | |
| `TIME_BOUNDARY` | `a.After(b)` to `!a.Before(b)`, `a.Before(b)` to `!a.After(b)`, and back | `CONDITIONALS_BOUNDARY` for a `time.Time`: each pair differs only when the two times are equal |
| `CALENDAR_DAY` | `t.AddDate(0, 0, n)` to `t.Add(time.Duration(n) * 24 * time.Hour)` | for a `time.Time`: a day across a change of daylight saving time is not 24 hours |
| `ARITHMETIC_BASE` | `+` to `-`, `-` to `+`, `*` to `/`, `/` to `*`, `%` to `*` | skips a `+` in a zerolog chain that ends in `Msg`, `Msgf` or `Send` |
| `INCREMENT_DECREMENT` | `++` to `--`, and back | |
| `INVERT_LOGICAL` | `&&` to `\|\|`, and back | |
| `REMOVE_LOGICAL_NOT` | `!x` to `x` | |
| `EXPRESSION_REMOVE` | `a && b` to `true && b` and to `a && true`, `a \|\| b` to `false \|\| b` and to `a \|\| false` | |
| `BRANCH_IF`, `BRANCH_ELSE`, `BRANCH_CASE` | the body of an `if` or an `else` to `{}`, and no statement in a `case` | finds an error branch that no test enters; skips a body that has only log calls |
| `STATEMENT_REMOVE` | `x = expr` to `_ = expr`, and removes a call that stands alone, such as `close(done)` or `wg.Done()` | skips a log call that ends in `Msg`, `Msgf` or `Send`, also over more than one line, and `panic` |
| `RETURN_ZERO` | a return value to the zero value of its type, and a struct literal `T{…}` to `T{}` | the type comes from `go/types` |
| `RETURN_ERROR_NIL` | an error return value to `nil` | `go/types` finds the error slot |
| `RETURN_TRUE` | a bool return value to `true` | `go/types` finds the bool slot; `RETURN_ZERO` already makes it `false` |
| `INTEGER_INCREMENT`, `INTEGER_DECREMENT` | `n` to `(n+1)`, `(n-1)` | |
| `RANGE_BREAK` | `break` at the start of a `range` body | |
| `BREAK_AT_END` | `break` at the end of a `for` body | a loop that keeps only its first item |
| `SWAP_FIELDS` | swap the values of two adjacent keyed fields of the same type | a hook; two equal figures hide a swap |
| `FIELD_ZERO` | removes one keyed field from a struct literal, so the field gets the zero value of its type | off by default until its noise is measured; shows a field that no test reads |
| `ARGUMENT_ZERO` | a call argument to the zero value of its parameter type | off by default: each argument of each call makes a mutant, so it is noisy until it has a filter |
| `ERRORF_WRAP` | `%w` to `%v` | off by default: on two measured commits it made 3 of 5 survivors, and no caller unwrapped those errors |

### Filters

A filter drops a candidate before it costs a build. The Go adapter has these:

| Filter | Drops |
| --- | --- |
| Same text | an edit whose replacement is the same as the original |
| Test file | an edit in a `_test.go` file |
| Generated code | a file with the `// Code generated ... DO NOT EDIT.` line |
| Build tags | an edit in a file that `go list` leaves out of its package with the build tags of the run |
| Type | a `SWAP_FIELDS` pair outside a struct literal, or whose two values do not have identical types in `go/types` |
| Table | a `SWAP_FIELDS` pair in a table of named values, where each value is a literal of one named type with constant fields, such as `NoMatch: Reason{"NoMatch"}`. A swap there lives unless a test reads the text. |
| Type | a `RETURN_ZERO` value that is not the zero value of its slot, that fills an error slot, or that is zero already |
| Type | a `RETURN_ERROR_NIL` value that does not fill an error slot, and a `RETURN_TRUE` value that does not fill a bool slot or is `true` already |
| Negative index | an `INTEGER_DECREMENT` of a literal `0` in an index, a slice bound or a size for `make` |
| Type | a `FIELD_ZERO` field outside a struct literal, or whose value is zero already |
| Type | an `ARGUMENT_ZERO` value that is not the zero value of its parameter, that fills an error parameter, or that is zero already, and an argument of a builtin, of a conversion, of a variadic parameter or of a log chain. Also a `context.Context`, and the constant text of a call whose only result is an error, such as `errors.New` or `fmt.Errorf`. |
| Type | a `TIME_BOUNDARY` or `CALENDAR_DAY` call of a method that `go/types` does not find on `time.Time`, and a `CALENDAR_DAY` in a file that does not import `time` by that name |

A candidate that passes the filters and still fails to build is NOT VIABLE.

### Mutant ids

```text
<file from the repository root>:<function>:<operator>#<n>
internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1
```

- **The function** comes from the adapter. For Go, it is the receiver and the name of the `FuncDecl` that
  holds the edit: `(*Handler).accounts`, `Handler.name` or `total`. A generic receiver drops its type
  parameters. Outside a function, it is the name of the declaration, for example a package variable.
- **`n`** counts the mutants of that operator in that function, in source order. It counts after the
  filters and before the scope, so `n` does not depend on the lines that the diff holds.

So an edit in another function does not change the id, and a mutant has the same id with `--base HEAD` and
with the merge base of the branch. An id with a line number changes each time the code above it moves, so a
survivor from the build step does not match a row in the audit.

### The Go runner

The runner builds the test binary with an overlay, and then runs the binary itself. It never runs `go test`
on a mutant.

```mermaid
sequenceDiagram
    participant W as worker
    participant R as Go runner
    participant G as go
    participant B as test binary
    W->>R: mutant
    R->>R: write the mutated file and overlay.json in a temp folder
    R->>G: go test -c -vet=off -overlay overlay.json -o pkg.test . (in the package folder)
    opt the build fails with an unused import or variable
        R->>G: the same build, with each unused import made blank and _ = x for each unused variable x
    end
    alt the build fails
        G-->>R: exit code not 0
        R-->>W: NOT VIABLE
    else the build passes
        R->>B: pkg.test -test.count=1 -test.failfast, new process group, in the package folder
        opt the limit ends first
            R->>B: SIGKILL to the process group
            R->>B: the same binary again, with twice the limit
        end
        alt the limit ends first
            R->>B: SIGKILL to the process group
            R-->>W: TIMED OUT
        else the binary exits
            B-->>R: exit code and output
            R-->>W: KILLED, LIVED or INFRA ERROR
        end
    end
```

- **No write to the work tree.** The mutated file and the overlay file stay in a temp folder. `-overlay`
  makes the build read the mutated file in place of the real one.
- **No test cache.** The runner runs the binary itself, so Go's test cache never gives a result.
- **A clean split.** A build failure comes from `go test -c`, and a test failure comes from the binary. The
  runner does not read `[build failed]` out of mixed output.
- **The package folder.** The binary runs in the package folder, as `go test` does, so a test can read its
  `testdata`.
- **Build tags.** `--tags` goes to `go list`, to the coverage run and to `go test -c`.
- **No vet.** `-vet=off` keeps a vet check out of the build, because a vet check is not a build failure.
- **Unused imports and variables.** A mutant that removes the only use of an import, such as the
  `fmt.Errorf` of an error branch, fails with "imported and not used". A mutant that removes the only use
  of a variable, such as `|| !slices.Contains(requested, id)`, fails with "declared and not used". The
  runner then makes each import that the compiler names blank, and adds `_ = x` for each variable `x` that
  it names: after the statement that declares `x`, or at the start of each body whose header declares `x`,
  such as `if v, ok := m[k]; ok {` or `case v := <-c:`. Then it builds one more time. Without this,
  `BRANCH_IF` on an error branch is NOT VIABLE. On 12 measured PRs, 56 of 78 NOT VIABLE mutants failed only
  with "declared and not used", and two of them live when they build.
- **No process stays alive.** Each build and each test binary runs in its own process group. At its limit,
  after it exits, and when `mutants` gets SIGINT or SIGTERM, the runner sends SIGKILL to the whole group.

### Statuses

```mermaid
flowchart TD
    S["mutant"] --> CV{"does a block with count 0 hold its start,<br/>with no covered block on its line?"}
    CV -- "yes" --> NC["NOT COVERED"]
    CV -- "no" --> BLD{"does the test binary build?"}
    BLD -- "no" --> NV["NOT VIABLE"]
    BLD -- "past --build-limit" --> IE
    BLD -- "yes" --> RUN{"how did the binary end?"}
    RUN -- "the limit ended first, two times" --> TO["TIMED OUT"]
    RUN -- "exit 0" --> LV["LIVED"]
    RUN -- "a test failed or panicked" --> KD["KILLED"]
    RUN -- "a signal or out of memory" --> IE["INFRA ERROR"]
```

| Status | Meaning | Counts as a survivor |
| --- | --- | --- |
| KILLED | a test failed | no |
| LIVED | every test passed | yes |
| NOT COVERED | no test runs the line | yes |
| NOT VIABLE | the mutant does not build | no |
| TIMED OUT | the tests ran past the limit | no |
| INFRA ERROR | the host stopped the run, for example out of memory, or the build ran past `--build-limit` | no |

Go's coverage profile has no block for the part of a statement that comes after a function literal. A tool
that reads "no block" as "not covered" never runs the mutants on those lines. So `mutants` marks a mutant
NOT COVERED only when a block with a count of 0 holds the line and the column of its start. A mutant that no
block holds runs.

A mutant on a line that a test runs also runs. So `BRANCH_IF` of an error branch that no test enters starts
on the line of its `if`, and is LIVED, while the `return` inside the branch is NOT COVERED.

### Time limits

| Limit | Value |
| --- | --- |
| The build of one mutant | 120 s, or `--build-limit` |
| The test run of one mutant | the baseline test time of its package × 3 + 5 s, and twice that for the second run |
| The whole run | `--limit`, none by default |

`mutants` measures the baseline once for each package, with the real code, before the first mutant. The
coverage run is the baseline, so the baseline includes the cost of `-cover`. A package whose baseline fails
stops the run with exit 2: a red suite gives no verdict.

The load of the host can grow after the baseline. A mutant that runs past its limit therefore runs one more
time with twice the limit, and it is TIMED OUT only when the second run also runs past the limit.

At the `--limit` of the whole run, `mutants` stops the workers, writes the rows that it has, and exits 124.
At SIGINT or SIGTERM, it stops the workers and exits 130.

### Workers

| Setting | Default | Notes |
| --- | --- | --- |
| `--workers` | 4 | one mutant at a time for each worker |
| `-p` in `GOFLAGS` | 2 | for each build, unless the user sets `GOFLAGS` |
| `GOMAXPROCS` of a test binary | CPUs ÷ workers | so the test binaries do not use all the CPUs at once |

The workers take the mutants in the order of their files, so the mutants of one package follow each other.
The first mutant of a package pays for the cold build, and the next mutants reuse the build cache. The
coverage runs of different packages run at the same time, as many as there are workers.

## Commands

| Command | Does | Exit |
| --- | --- | --- |
| `mutants run` | runs the mutants of the changed lines | 0 no survivor, 10 survivors, 124 the limit, 2 a usage or tool error, 130 SIGINT or SIGTERM |
| `mutants run --all FOLDER...` | runs the mutants of whole packages | same |
| `mutants rerun ID` | runs one mutant again, with no cache | 0 killed, 10 lived or not covered, 1 no verdict, 2 an unknown id |
| `mutants operators` | lists the operators and their rules | 0 |
| `mutants version`, `mutants update` | as now | |

The flags of `run`: `--base`, `--workers`, `--limit`, `--build-limit`, `--tags`, `--operators`,
`--format rows|json`, `--json PATH`, `--stryker PATH`. The flags of `rerun`: `--tags`, `--build-limit`.

`rerun` finds the mutant with all the rules of its operator, also an operator that is off by default, and
with no diff. Then it runs the coverage and the mutant as `run` does, and prints one row with the reason
for the status.

## Output

The rows are for a person or an agent. Each row is one mutant that needs a look, grouped by status. A KILLED
or a NOT VIABLE mutant gets no row:

```text
LIVED:
  internal/order/handler.go:42 BRANCH_IF: { return nil, fmt.Errorf("load the accounts ... -> {}  [internal/order/handler.go:(*Handler).accounts:BRANCH_IF#1]
NOT COVERED:
  internal/order/handler.go:43 RETURN_ERROR_NIL: fmt.Errorf("load the accounts ... -> nil  [internal/order/handler.go:(*Handler).accounts:RETURN_ERROR_NIL#1]
mutants: 81, killed: 64, lived: 1, not covered: 3, not viable: 13 (base 1a2b3c4d5e)
```

Each row shows the original and the replacement on one line, cut at 40 characters. When both are long, both
start a few words before the first difference, so that the row shows it. An empty replacement shows as
`(nothing)`.

A package with no test files gives one row for all its mutants, not one row for each mutant:

```text
NOT COVERED:
  package cmd/evalreport has no test files: 502 mutants
```

`--format json` prints every mutant as one JSON document: `base`, and `mutants` with the fields `id`, `file`,
`line`, `column`, `operator`, `status`, `original`, `replacement` and `detail`, the reason for the status.
`--stryker` writes version 2 of the `mutation-testing-elements` report format, which has an HTML viewer and
does not depend on the language. The progress lines and the messages go to stderr, so they do not mix with
the JSON on stdout.

## Config

A repository can keep its settings in `.mutants.yml` at its root. A flag wins over the file. An unknown key
is an error that names the key, its line and the known keys.

```yaml
base: origin/main
workers: 4
tags: [unit]
operators: [-ERRORF_WRAP]
exclude: ["**/*_gen.go", "vendor/**"]
zero_functions: [maybe.None, caseautoresolve.Submitted]
```

`zero_functions` names the functions that return the zero value of their type, as `package.Function` with the
name of the package, not its path. `FIELD_ZERO` skips a field whose value is a call of one of them, because
the removal of that field changes nothing.

v1 keeps no cache and no state file. The rules for a scan, the mutated files, the overlays, the test binaries
and the coverage profiles go to temp folders that `mutants` removes after each use. The v2 cache goes under
`$(git rev-parse --git-dir)/mutants/`, so the work tree stays clean.

## Packages

```mermaid
flowchart TD
    CMD["internal/cmd<br/>run, rerun, operators"]
    RUN["internal/run<br/>one run, from the changed lines to the verdicts"]
    DIFF["internal/diff<br/>diff.Lines, diff.Repository"]
    OP["internal/operator<br/>operator.Rule, operator.Hook, operator.Matcher"]
    AG["internal/operator/astgrep<br/>the ast-grep CLI"]
    MUT["internal/mutant<br/>mutant.Mutant, mutant.ID, mutant.Status, mutant.Runner"]
    LANG["internal/language<br/>language.Adapter"]
    GO["internal/language/golang<br/>go list, go/types, coverage, the Go runner"]
    REP["internal/report<br/>rows, JSON, Stryker"]
    CMD --> RUN
    CMD --> AG
    CMD --> GO
    CMD --> REP
    RUN --> DIFF
    RUN --> OP
    RUN --> LANG
    RUN --> MUT
    AG --> OP
    GO --> LANG
    LANG --> OP
    LANG --> MUT
    REP --> MUT
```

An arrow means "imports". `cmd` gives the ast-grep matcher and the Go adapter to `run`, so `run` knows only
the interfaces. `cmd` writes the reports from the mutants that `run` gives back.

| Package | Holds |
| --- | --- |
| `diff` | `diff.Lines`, the changed lines of each file, and `diff.Repository`, the git calls that read them |
| `operator` | the rule packs, `operator.Rule`, `operator.Hook`, `operator.Matcher`, and the hooks |
| `operator/astgrep` | an `operator.Matcher` that calls `ast-grep scan --json` and parses its matches |
| `mutant` | `mutant.Mutant`, `mutant.Status`, `mutant.Runner`, and `mutant.ID` with the `mutant.Counter` that numbers the ids |
| `language` | `language.Adapter`: the name of its rule pack, the files it supports, its filters, the function that holds an offset, its coverage and its runner |
| `language/golang` | the Go adapter |
| `run` | one run: changed lines, candidates, filters, ids, scope, coverage and workers |
| `report` | the rows, the JSON and the Stryker format |

```go
package language

// Adapter is everything that the core needs from one language. A file is a path from the root of the
// repository.
type Adapter interface {
	Name() string
	Extensions() []string
	Keep(candidate operator.Edit) bool
	Function(file string, offset int) string
	// Uncovered gives the mutants that no test runs. The value is empty, or a reason that holds for each
	// mutant of one package.
	Uncovered(ctx context.Context, mutants []mutant.Mutant) (map[mutant.ID]string, error)
	Runner() mutant.Runner
}
```

ast-grep is a separate binary. mise pins its version beside the other tools, and `mutants` checks the
version when it starts.

## Tests of the tool

| Level | What it checks |
| --- | --- |
| Unit (`-tags=unit`) | the parse of a diff with each prefix setting, the scope of edits, the ids, the filters, the status of each exit, each rule against a small Go source. The tests of `run` use the real rules and ast-grep, and a fake `language.Adapter`. |
| End to end (`e2e/`) | the binary against small Go modules in git repositories that the tests make, with known answers |
| Acceptance | a real branch with known test gaps, two runs, compared mutant by mutant |

The end-to-end fixtures:

| Fixture | Expected |
| --- | --- |
| Two figures of one type with equal values in the test | `SWAP_FIELDS` LIVED. With different values in the test: KILLED. |
| An error branch that no test enters | `BRANCH_IF` LIVED and `RETURN_ERROR_NIL` NOT COVERED |
| A condition that holds the only use of a variable and of an import | `EXPRESSION_REMOVE` LIVED, not NOT VIABLE |
| A deadline that a test checks one hour before and one hour after | `TIME_BOUNDARY` LIVED. With a check at the deadline itself: KILLED. |
| A due date three calendar days after a start, with a test in UTC | `CALENDAR_DAY` LIVED. With a test in Sydney across the start of daylight saving time: KILLED. |
| A loop over a list that a test runs with one item | `BREAK_AT_END` LIVED. With two items: KILLED. |
| A busy loop and a removed `close` | TIMED OUT for both, and the child process of the test is not alive after the run |
| `diff.mnemonicPrefix=true` in the git config of the fixture | the same mutants as without it |
| An untracked new file | its mutants run, and `git status` is the same after the run |
| A folder name that differs from its package name | the mutants run the tests of that package |
| A test file with a build tag | with `--tags`, its tests run |
| Two runs | the same verdicts, mutant by mutant |
| A package with no test files | one row with the count of its mutants, and each mutant in the JSON |

## Later

| Version | Adds |
| --- | --- |
| v2 | **Test selection by coverage.** A map from each line to the tests that run it, so a mutant runs only those tests. This was the main speed gain of gomutants. |
| v2 | **A cache.** A verdict keyed by a hash of the package files, the test files, the rules and the `mutants` version, so a second run reuses it. |
| v3 | **TypeScript.** A rule pack, and a runner that writes each mutant into a git worktree for each worker, in a temp folder, and runs `vitest related <file> --run`. |
| v3 | Other languages that ast-grep parses: the same shape, a rule pack and a runner. |

## Decisions

**Why ast-grep rather than tree-sitter.** ast-grep is tree-sitter plus a pattern language, a rewriter and the
grammars. With tree-sitter alone, `mutants` has to build those three again. The rules then stay data that a
user can add to without a new release.

**Why the ast-grep CLI rather than a library.** ast-grep has no Go binding. One call returns all the matches
as JSON, and the parse takes much less time than one test run.

**Why `mutants` starts ast-grep rather than read its matches from a pipe.** With a pipe, an ast-grep that
fails gives empty input. Unless the shell has `pipefail` on, the pipe returns exit 0, and `mutants` reports a
clean result for 0 mutants. That is the same fault as a diff with the wrong prefix. When `mutants` starts
ast-grep, it reads the exit code, stderr and the version. A pipe also leaves the hard parts to the caller: the
changed files, and the rule packs that live in the binary.

A pipe does not remove the tie to ast-grep. `mutants` still parses the JSON of ast-grep, because the hooks,
the filters and the ids take matches as input. With a pipe, `rerun ID` needs the user to repeat the exact
ast-grep call, and an implementation flow gets two commands and two exit codes. A unit test can give `run` a
fake `operator.Matcher` with fixed matches, so a test of the core does not need ast-grep. Another generator, for example
comby, is a second `operator.Matcher`.

**Why the binary rather than `go test`.** The binary gives a test failure and a build failure from two
different commands, and Go's test cache never answers.

**Why the whole package in v1 rather than the tests that cover a line.** A run of the whole package gives a
correct verdict with no map. For a branch of about 100 mutants, it takes about 1 to 2 minutes with 4 workers.
The map comes in v2, for speed.

**Why the coverage takes mutants rather than files.** A line is not enough to say that no test runs a
mutant. The code after a function literal shares a line with the end of the block of the literal, and the
profile has no block for that code. So the adapter needs the line and the column of each mutant, and
returns the ids of the mutants that no test runs.

**Why the ids count after the filters and before the scope.** The scope depends on the diff, and the diff
of the build step differs from the diff of the audit. When `n` counts only the mutants in scope, one mutant
gets two ids. The filters depend only on the code, so the ids can count after them, and have no gaps.

**Why the limit has a margin and a second run.** A limit of only 3 × the baseline gave false TIMED OUT
verdicts on the measured monorepo, under a load average of 10 to 20: 3 × 1.2 s for a mutant that a test
kills in 0.55 s alone. PIT adds 4 s to its factor, and Stryker adds 5 s. A second run with twice the limit
costs time only for a mutant that really hangs, and such a mutant is rare.

**Why RETURN_TRUE is its own operator.** `true` is not a zero value, so it does not belong in `RETURN_ZERO`.
Without it, a guard that returns `false` gets no mutant.

**Why read untracked files rather than add them to the index.** A change to the index can stay behind when a
run stops halfway, and the user then sees files that they did not stage.
