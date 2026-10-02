# What Each Operator of mutants Changes

An operator is one kind of change. Each row of the output names the operator of its mutant. The operators
are the same in each language: the catalog in `internal/operator/catalog.go` names them, and the rule pack
of each language gives the syntax of each one. This page tells what each operator changes, with an example
in Go, and what a survivor of that operator usually shows.

`mutants operators` lists the operators of your version, whether each one runs by default, and the
ast-grep rules of each one.

## Comparisons

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `CONDITIONALS_BOUNDARY` | `total >= 100` to `total > 100`, `<` to `<=`, and `now.After(deadline)` to `!now.Before(deadline)` | no test uses the value at the boundary, such as an order of exactly 100 or the exact time of a deadline |
| `CONDITIONALS_NEGATION` | `a == b` to `a != b`, and `<` to `>=` | no test checks the result of the comparison |

## Logic

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `INVERT_LOGICAL` | `a && b` to `a \|\| b` | no test makes one side true and the other side false |
| `EXPRESSION_REMOVE` | `a && b` to `true && b`, and `a \|\| b` to `false \|\| b` | one part of a condition has no test of its own |
| `REMOVE_LOGICAL_NOT` | `!ok` to `ok` | no test checks the condition in both states |

## Numbers

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `ARITHMETIC_BASE` | `a + b` to `a - b`, `a * b` to `a / b` | no test checks the result of the calculation |
| `INCREMENT_DECREMENT` | `i++` to `i--` | no test checks the count |
| `INTEGER_INCREMENT`, `INTEGER_DECREMENT` | `3` to `(3+1)` and to `(3-1)` | no test pins the number, for example a limit or a size |

## Branches and statements

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `BRANCH_IF`, `BRANCH_ELSE` | the body of an `if` or of an `else` to `{}` | no test enters the branch, or no test checks what the branch does |
| `BRANCH_CASE` | no statement in one `case` of a `switch` or a `select` | no test enters the case |
| `STATEMENT_REMOVE` | `p.total = sum` to `_ = sum`, and `close(done)` removed | no test checks the effect of the statement |

## Return values

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `RETURN_EMPTY` | `return total, nil` to `return 0, nil`, and `return Trigger{kind: note}` to `return Trigger{}` | no test checks the value that the function returns |
| `ERROR_REMOVE` | `return fmt.Errorf("load: %w", err)` to `return nil` | no test checks that the function returns an error |
| `RETURN_TRUE` | `return a < b` to `return true` | no test expects `false` from the function |

## Loops

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `BREAK_AT_START` | `break` at the start of the body of a `range` loop | no test checks what the loop does |
| `BREAK_AT_END` | `break` at the end of the body of a `for` loop | the tests give the loop only one item |

## Named values

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `NAMED_VALUE_SWAP` | `Totals{Paid: paid, Owed: owed}` to `Totals{Paid: owed, Owed: paid}` | the tests use two equal values, so a swap gives the same result |
| `NAMED_VALUE_REMOVE` | `Info{Arrived: arrived, ID: id}` to `Info{ID: id}`, so `Arrived` gets its zero value | no test reads the field |

## Operators that are off by default

These operators run only when `--operators` or `.mutants.yml` names them, for example
`mutants run --operators=+ARGUMENT_EMPTY`. They give more survivors that are not gaps in the tests.

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `ARGUMENT_EMPTY` | `resolve(input.ClientID.String())` to `resolve("")` | no test sends the value through the call |
| `ERROR_CAUSE_REMOVE` | `fmt.Errorf("load: %w", err)` to `fmt.Errorf("load: %v", err)` | no caller checks the error with `errors.Is` or `errors.As` |

## What mutants skips

`mutants` does not make a mutant that cannot build, or that changes nothing a test can see. For example:

- a log line that ends in `Msg`, `Msgf` or `Send`, and a branch that holds only log lines
- a change in a `_test.go` file or in generated code
- a value that is already the zero value of its type
- a swap of two values with different types, or in a table of named values such as
  `NoMatch: Reason{"NoMatch"}`

[The filters in the design](design.md#filters) list each case.

## Operators of your own

A repository can add its own operators as ast-grep rules. [Your own operators](usage.md#your-own-operators)
tells how.

For example, this rule changes `t.AddDate(0, 0, n)` in Go, which adds `n` calendar days, to an addition of
24 hours for each day. A survivor shows that no test crosses a change of daylight saving time, where a day
is not 24 hours. Put the rule in `.mutants/operators/go/CALENDAR_DAY.yml`:

```yaml
id: CALENDAR_DAY
language: go
rule:
  pattern:
    context: 'func f() { _ = $T.AddDate(0, 0, $N) }'
    selector: call_expression
fix: $T.Add(time.Duration($N) * 24 * time.Hour)
```

`mutants` has no type check for this rule. When `$T` is not a `time.Time`, or when the file does not import
`time` by that name, the mutant does not build and gets no row.
