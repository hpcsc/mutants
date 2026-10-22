# What Each Operator of mutants Changes

An operator is one kind of change. Each row of the output names the operator of its mutant. An operator is
the same change in each language that has it: the catalog in `internal/operator/catalog.go` names them, and
the rule pack of each language gives the syntax of each one. Each language has each required operator,
unless it has no such code and the catalog gives the reason. An optional operator is only in the languages
whose code it fits. Each operator except `GUARD_REMOVE` and `RETURN_ERROR` is required, so Go, Python and Elixir have each of them. Only Elixir has a rule for the optional `GUARD_REMOVE` and `RETURN_ERROR`. This page tells
what each operator changes, with an example in Go, and what a survivor of that operator usually shows.

`mutants operators` lists the operators of your version, whether each one runs by default, and the
ast-grep rules of each one. When the catalog leaves an operator out of a language, the list shows `none` and
the reason.

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
| `INCREMENT_DECREMENT` | `i++` to `i--`, and `total += n` to `total -= n` | no test checks the count or the total |
| `INTEGER_INCREMENT`, `INTEGER_DECREMENT` | `3` to `(3+1)` and to `(3-1)`. A limit of a comparison of integers moves only to the side that `CONDITIONALS_BOUNDARY` does not test: `total >= 100` gets `(100-1)`, because `total >= (100+1)` is `total > 100`. | no test pins the number, for example a limit or a size |
| `INVERT_NEGATIVES` | `-offset` to `offset` | no test checks the sign, for example a time before now against a time after it |

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

## Calls

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `INVERT_CALL` | `strings.HasPrefix` to `strings.HasSuffix`, `strings.Index` to `strings.LastIndex`, `slices.Min` to `slices.Max`, and `min` to `max` | no test tells the call from its opposite, for example a text that starts and ends with the same word, or a list with one item |

## Named values

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `NAMED_VALUE_SWAP` | `Totals{Paid: paid, Owed: owed}` to `Totals{Paid: owed, Owed: paid}` | the tests use two equal values, so a swap gives the same result |
| `NAMED_VALUE_REMOVE` | `Info{Arrived: arrived, ID: id}` to `Info{ID: id}`, so `Arrived` gets its zero value, and `map[string]string{"general": "dispute", "fraud": "fraud"}` to `map[string]string{"fraud": "fraud"}` | no test reads the field, or no test looks up the key |

## In Python

The operators make the same changes in Python:

| Operator | Example in Python |
| --- | --- |
| `CONDITIONALS_BOUNDARY` | `total >= 100` to `total > 100`. A chain such as `0 < x < 10` gets no mutant. |
| `CONDITIONALS_NEGATION` | `a == b` to `a != b`, `a is None` to `a is not None`, and `a in xs` to `a not in xs` |
| `INVERT_LOGICAL`, `EXPRESSION_REMOVE`, `REMOVE_LOGICAL_NOT` | `a and b` to `a or b`, `a and b` to `True and b`, and `not ok` to `ok` |
| `ARITHMETIC_BASE` | `a + b` to `a - b`, and `a // b` to `a * b`. A `+` or `*` with a string, such as `"id: " + x`, and the `%` of `"%s" % x` get no mutant, because the change always raises `TypeError`. |
| `INCREMENT_DECREMENT` | `count += 1` to `count -= 1` |
| `INTEGER_INCREMENT`, `INTEGER_DECREMENT` | `3` to `(3+1)` and to `(3-1)`. A number in the pattern of a `case` gets no mutant, because Python does not compile `case (3+1):`. A limit of a comparison moves only to the side that `CONDITIONALS_BOUNDARY` does not test, also when the other side is a float. |
| `INVERT_NEGATIVES` | `-offset` to `offset`, and `items[-1]` to `items[1]` |
| `INVERT_CALL` | `min` to `max`, `any` to `all`, and the methods `startswith` to `endswith`, `lstrip` to `rstrip`, `upper` to `lower`, `removeprefix` to `removesuffix` and `find` to `rfind`, with the reverse of each. A method of another type with the same name also gets the mutant. |
| `BRANCH_IF`, `BRANCH_ELSE`, `BRANCH_CASE` | the body of an `if`, an `elif`, an `else` or a `case` to `pass` |
| `STATEMENT_REMOVE` | `self.total = s` to `_ = s` and `s = s + item` to `_ = s + item` in a function, and a call that stands alone, such as `client.close()`, to `pass`. An assignment gets no mutant when its name has no value before it in the function: no parameter, no earlier assignment, no `for` or `with` target, and no `global` or `nonlocal`. The next read of the name then raises `NameError`. |
| `RETURN_EMPTY` | `return total` to `return 0` in a function with `-> int`, and to `return None` in a function with no return type |
| `ERROR_REMOVE` | `raise ValueError(x)` to `return` in a function |
| `RETURN_TRUE` | `return a < b` to `return True` |
| `BREAK_AT_START`, `BREAK_AT_END` | `break` at the start of the body of a `for` loop, and at the end of the body of a `for` or `while` loop |
| `NAMED_VALUE_SWAP`, `NAMED_VALUE_REMOVE` | `Totals(paid=paid, owed=owed)` to `Totals(paid=owed, owed=paid)`, and to `Totals(owed=owed)` |
| `ARGUMENT_EMPTY` (off by default) | `resolve(client_id)` to `resolve(None)` |
| `ERROR_CAUSE_REMOVE` (off by default) | `raise LoadError(x) from err` to `raise LoadError(x)` |
| `CONDITION_TRUE` (off by default) | `if key not in seen:` to `if True:` in an `if` with no `elif` or `else`, and the condition of the last `elif` when no `else` follows |

## In Elixir

The operators make the same changes in Elixir. Elixir has no statement without a value, so an operator that
removes code puts `nil` in its place:

| Operator | Example in Elixir |
| --- | --- |
| `CONDITIONALS_BOUNDARY` | `total >= 100` to `total > 100`, and `DateTime.after?(now, due)` to `not DateTime.before?(now, due)` |
| `CONDITIONALS_NEGATION` | `a == b` to `a != b`, `a === b` to `a !== b`, and `a in xs` to `a not in xs` |
| `INVERT_LOGICAL`, `EXPRESSION_REMOVE`, `REMOVE_LOGICAL_NOT` | `a and b` to `a or b`, `a && b` to `true && b`, and `not ok` or `!ok` to `ok` |
| `ARITHMETIC_BASE` | `a + b` to `a - b`, and `div(a, b)` to `(a * b)`. The `/` of a capture such as `&f/1` gets no mutant. |
| `INCREMENT_DECREMENT` | `count = count + 1` to `count = count - 1`. `ARITHMETIC_BASE` makes no mutant of this `+`. |
| `INTEGER_INCREMENT`, `INTEGER_DECREMENT` | `3` to `(3+1)` and to `(3-1)`. A number in a pattern, such as a function head, a `case` clause or the left side of `=`, gets no mutant, because Elixir does not compile `(3+1)` there. A number in a guard gets mutants. A limit of a comparison moves only to the side that `CONDITIONALS_BOUNDARY` does not test, also when the other side is a float. |
| `INVERT_NEGATIVES` | `DateTime.add(now, -amount, unit)` to `DateTime.add(now, amount, unit)`. The `-` of a bitstring segment, such as `<<n::signed-64>>`, is no minus, so it gets no mutant. |
| `INVERT_CALL` | `Enum.filter` to `Enum.reject`, also in a pipe, `Enum.all?` to `Enum.any?`, `Enum.take` to `Enum.drop`, `Enum.max_by` to `Enum.min_by`, `List.first` to `List.last`, `String.starts_with?` to `String.ends_with?` and `String.upcase` to `String.downcase`, with the reverse of each, and the same calls of `Stream`. A capture such as `&Enum.take/2` gets no mutant. |
| `BRANCH_IF`, `BRANCH_ELSE`, `BRANCH_CASE` | the body of an `if`, an `unless`, an `else`, or a clause of `case`, `cond`, `receive` or the `else` of `with` to `nil`, in a function. An `if` outside a function chooses the functions of the module when it compiles, so it gets no mutant. |
| `STATEMENT_REMOVE` | `total = total * 2` to `_ = total * 2`, and a call or a pipe that stands alone, such as `send(pid, :done)`, to `nil`. The last expression of a body gets no mutant, because it gives the value of the body. Each step of a pipe in a function is removed, the last step too, such as `\|> halt()` of `conn \|> redirect(to: path) \|> halt()`. A binding gets no mutant when its variable has no value before it in the function, because the next read of the variable does not compile. |
| `RETURN_EMPTY` | the last value of a function to `0` in a function whose `@spec` returns `integer()`, to `false` in a function whose name ends in `?`, and to `nil` in a function with no `@spec`. When an `if` or a `case` gives the value of the function, the last value of each branch gets the mutant. A value that is alone in its branch gets no `nil`, because `BRANCH_IF`, `BRANCH_ELSE` and `BRANCH_CASE` already put `nil` there. |
| `ERROR_REMOVE` | `raise ArgumentError` to `nil` in a function, and an error tuple that a function returns, such as `{:error, :not_found}`, to `:ok` |
| `RETURN_TRUE` | a comparison, a `not` or `false` that a function returns to `true`, and each other value in a function that returns `boolean()` or whose name ends in `?` |
| `BREAK_AT_START`, `BREAK_AT_END` | `x <- xs` of a `for` to `x <- []` and to `x <- Enum.take(xs, 1)`, and the list of `Enum.each`, `Enum.map`, `Enum.reduce` and the other loops of `Enum` to no item and to only its first item, in a function. A loop outside a function makes the functions of the module when it compiles, so it gets no mutant. |
| `NAMED_VALUE_SWAP`, `NAMED_VALUE_REMOVE` | `%Totals{paid: paid, owed: owed}` to `%Totals{paid: owed, owed: paid}`, and to `%Totals{owed: owed}`. A call keeps at least one of its keywords, because a call without its keywords has another arity. An Ecto `from` loses its only keyword, such as `where:` in `from(c in Code, where: c.at < ^t)`, because its keywords are optional. A field of a pattern that binds a variable, such as `coef: coef`, stays, because the next read of the variable does not compile. A field of a struct whose value is its default in `defstruct` also stays. |
| `GUARD_REMOVE` | `def load(key) when is_binary(key) do` to `def load(key) do`, and the guard of a clause of `case`, `fn` or `with` removed, in a function. A survivor shows that no test gives the clause a value that the guard refuses. Only Elixir has this operator. |
| `RETURN_ERROR` | an ok tuple that a function returns, such as `{:ok, user}`, to `{:error, user}`. `RETURN_EMPTY` puts `nil` there, and the `case` or the `with` of the caller then raises, so almost each test kills it. A caller handles `{:error, user}`, so a survivor shows that no test checks what the caller does with the success, for example the reply or the state after the call. An ok tuple in a pattern, or before the last expression of a body, gets no mutant. Only Elixir has this operator. |
| `ARGUMENT_EMPTY` (off by default) | `resolve(client_id)` to `resolve(nil)` |
| `ERROR_CAUSE_REMOVE` (off by default) | `reraise error, __STACKTRACE__` to `raise(error)`, which loses the stacktrace |
| `CONDITION_TRUE` (off by default) | `if key not in seen do` to `if true do`, and `if ok, do: x` to `if true, do: x`, in an `if` with no `else`, in a function. An `unless` gets no mutant, because a true condition there gives the same result as `BRANCH_IF`. |

## Operators that are off by default

These operators run only when `--operators` or `.mutants.yml` names them, for example
`mutants run --operators=+ARGUMENT_EMPTY`. They give more survivors that are not gaps in the tests.

| Operator | Example | A survivor usually shows |
| --- | --- | --- |
| `ARGUMENT_EMPTY` | `resolve(input.ClientID.String())` to `resolve("")` | no test sends the value through the call |
| `ERROR_CAUSE_REMOVE` | `fmt.Errorf("load: %w", err)` to `fmt.Errorf("load: %v", err)` | no caller checks the error with `errors.Is` or `errors.As` |
| `CONDITION_TRUE` | `if !slices.Contains(ids, id) {` to `if true {`, in an `if` with no `else`. An `if` with an `else` gets no mutant, because `CONDITIONALS_NEGATION` and `BRANCH_ELSE` already test both sides. | no test makes the condition false and checks that the body does not run, for example a second call with the same id |

## What mutants skips

`mutants` does not make a mutant that cannot build, or that changes nothing a test can see. For example:

- a change in a zerolog line that ends in `Msg`, `Msgf` or `Send`, or in an assignment such as
  `logEvent = logEvent.Str("id", id)` that adds fields to the event that it assigns, and the removal of a
  branch that holds only such lines. An `if` whose branches hold only such lines gets no mutant, when its
  condition calls no function.
- a change by `RETURN_EMPTY` or `RETURN_TRUE` of a value that a return gives with an error that is not nil,
  such as the `false` of `return false, err` in `if err != nil`, because a caller reads the error first
- a change in a `_test.go` file, in a Python test file, in an Elixir file in a `test` folder, or in generated
  code
- a change in a call of a Python logger, in a type annotation, in an `if TYPE_CHECKING:` block, or in a
  comparison with `sys.version_info`
- a change in a call of Elixir's `Logger`, in a typespec, in `import`, `alias` or `require`, in `@compile`
  or `@dialyzer`, in a capture such as `&f/1`, or in a macro, a guard, `defstruct` or `defexception`
- the move of an integer limit to the side that `CONDITIONALS_BOUNDARY` already tests, such as
  `total >= (100+1)`, which is `total > 100` for an integer
- the removal of a branch that its function needs to end, such as one case of a `switch` whose cases each
  return
- a value that is already the zero value of its type, except the value of an entry of a set such as
  `map[string]struct{}`
- a swap of two values with different types, or in a table of named values such as
  `NoMatch: Reason{"NoMatch"}`

[The filters in the design](design.md#filters) list each case. A repository can also skip other code, for
example the calls of its logger, as [Code that mutants skips](usage.md#code-that-mutants-skips) tells.

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
