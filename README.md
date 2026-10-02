# mutants

`mutants` finds the code that your tests run but do not check. It puts one small change at a time into the
changed lines of your branch, and it runs your tests after each change. When the tests still pass,
`mutants` shows the line and the change, so you know which test to add.

- It works on Go code in a git repository.
- It tests only the lines that your branch changes. A run then takes minutes, so you can run it before each
  review.
- By default, it compares your work tree with the point where your branch left `origin/HEAD`. So it also
  tests the changes that you did not commit, and each line of a new file that git does not track.
- It never writes to your files or to the git index.

<details>
<summary><strong>What mutation testing is, with an example</strong></summary>

Test coverage tells you that a test ran a line. It does not tell you that a test checked what the line
does. Mutation testing checks the tests: it puts a small bug in the code, and a good test fails.

This function gives a discount of 10 on an order of 100 or more:

```go
func Discount(total int) int {
	if total >= 100 {
		return 10
	}
	return 0
}
```

This test runs every line of `Discount`, so its coverage is 100%:

```go
func TestDiscount(t *testing.T) {
	if got := Discount(50); got != 0 {
		t.Errorf("Discount(50) = %d, want 0", got)
	}
	if got := Discount(150); got != 10 {
		t.Errorf("Discount(150) = %d, want 10", got)
	}
}
```

Now replace `total >= 100` with `total > 100`. An order of exactly 100 then gets no discount, which is a bug.
The test still passes, because no test uses an order of 100. This change is a mutant, and the mutant lived.

| Word | Meaning |
| --- | --- |
| mutant | one small change to the code, for example `>=` to `>` |
| operator | one kind of change. `CONDITIONALS_BOUNDARY` replaces `>=` with `>`, `<` with `<=`, and the reverse. |
| killed | a test failed with the mutant, so a test checks this behaviour |
| lived | every test passed with the mutant, so no test checks this behaviour |
| not covered | no test runs the line of the mutant |
| survivor | a mutant that lived or that is not covered |

</details>

## Install

`mutants` needs these tools on your `PATH`:

- git 2.30 or later
- Go
- [ast-grep](https://ast-grep.github.io/guide/quick-start.html) 0.45.0 or later, for example from
  `brew install ast-grep`

Install the latest release of `mutants` with this script:

```shell
sh <(curl -fsSL https://raw.githubusercontent.com/hpcsc/mutants/main/scripts/install.sh)
```

The script asks for the release channel and the install folder. [docs/install.md](docs/install.md) tells
its options, and how to update `mutants`.

## Your first run

1. Go to a git repository that holds a Go module, and check out your branch.
2. Run `mutants`:

   ```shell
   mutants run
   ```

3. Read the rows. This is the output for the `Discount` function of the example above, in the file
   `shop/discount.go`:

   ```text
   LIVED:
     shop/discount.go:5 CONDITIONALS_BOUNDARY: total >= 100 -> total > 100  [shop/discount.go:Discount:CONDITIONALS_BOUNDARY#1]
     shop/discount.go:5 INTEGER_DECREMENT: 100 -> (100-1)  [shop/discount.go:Discount:INTEGER_DECREMENT#1]
     shop/discount.go:5 INTEGER_INCREMENT: 100 -> (100+1)  [shop/discount.go:Discount:INTEGER_INCREMENT#1]
   mutants: 10, killed: 7, lived: 3 (base 4eea634536)
   ```

   Each row gives the file and the line, the operator, the code before and after the change, and the id
   of the mutant in `[ ]`. The last line counts the mutants of each status.

4. Find the gap. The three survivors show one gap: no test uses an order near 100. A test on
   each side of the boundary kills all three:

   ```go
   if got := Discount(99); got != 0 {
   	t.Errorf("Discount(99) = %d, want 0", got)
   }
   if got := Discount(100); got != 10 {
   	t.Errorf("Discount(100) = %d, want 10", got)
   }
   ```

5. Check one mutant again with its id:

   ```shell
   mutants rerun 'shop/discount.go:Discount:CONDITIONALS_BOUNDARY#1'
   ```

   ```text
   KILLED: shop/discount.go:5 CONDITIONALS_BOUNDARY: total >= 100 -> total > 100  [shop/discount.go:Discount:CONDITIONALS_BOUNDARY#1]
     --- FAIL: TestDiscount (0.00s)
   ```

6. Run all the mutants again. The last line is now `mutants: 10, killed: 10`.

## What to do with each status

A survivor is a mutant that lived or that is not covered. Each survivor shows a gap in the tests: a test that
you did not write, or an assertion that is too weak. When you add the test, the test kills the mutant.

| Status | Meaning | What to do |
| --- | --- | --- |
| LIVED | every test passed with the mutant | Add a test that fails with the mutant, or make an assertion stricter. |
| NOT COVERED | no test runs the line | Add a test that runs the line. |
| TIMED OUT | the tests with the mutant ran past the time limit, for example in an endless loop | Nothing. A mutant that makes the tests hang counts as found. |
| INFRA ERROR | the computer stopped the run, for example when it had no more memory | Run the mutant again with `mutants rerun`. |

A mutant that a test killed, and a mutant that does not build, get no row.

<details>
<summary><strong>When a survivor is not a gap in the tests</strong></summary>

Some mutants make no difference that a test can see. For example, `FIELD_ZERO` removes one field from a
struct literal. When the value of that field is already its zero value, the mutant gives the same result as
the real code. `mutants` already skips many of these mutants, such as the mutants of log lines. When you find
one:

- Leave it, and tell your reviewer why.
- Take its operator out of one run: `mutants run --operators=-FIELD_ZERO`.
- Take its operator out of each run in the repository, in `.mutants.yml` at the root:
  `operators: [-FIELD_ZERO]`.
- Name a function that returns a zero value, such as `maybe.None`, in `.mutants.yml`:
  `zero_functions: [maybe.None]`.

</details>

## Common commands

```shell
mutants run                          # the changed lines of your branch
mutants run --base HEAD              # only the changes that you did not commit
mutants run --all ./internal/order   # each line of one package; ./internal/... adds the subfolders
mutants rerun ID                     # one mutant again, by the id at the end of its row
mutants operators                    # each operator, and whether it runs by default
mutants config init                  # write a first .mutants.yml, with the base and the test tags
mutants run --proposals bugs.jsonl   # also run the bugs that an agent proposes, see docs/usage.md
mutants run --caller-gaps            # also find new code that no test of a changed caller runs
```

`mutants run` exits with 0 when no mutant survives. It exits with 10 when a mutant survives, or when the run
finds a caller gap. A CI step or a script can use this exit code.

## More documents

| Document | Tells |
| --- | --- |
| [docs/usage.md](docs/usage.md) | each command, flag, exit code, report format and setting of `.mutants.yml` |
| [docs/operators.md](docs/operators.md) | what each operator changes, with an example |
| [docs/install.md](docs/install.md) | the options of the install script, the update of `mutants` and its version |
| [docs/development.md](docs/development.md) | how to build, test and release `mutants` |
| [docs/design.md](docs/design.md) | how `mutants` works, and why |
| [docs/language-adapters.md](docs/language-adapters.md) | how to add a language to `mutants` |
| [docs/e2e-tests.md](docs/e2e-tests.md) | how the end-to-end tests work |
