import { describe, expect, it } from 'vitest'
import { goRepository, pythonRepository, type ReportedMutant, runMutants, writeFiles } from '../testUtils'

const goShop = `package shop

import (
	"errors"
	"fmt"
	"time"
)

type Totals struct {
	Paid, Owed int
}

type Order struct {
	ID    string
	Total int
}

type event struct{}

func (e *event) Str(key, value string) *event { return e }
func (e *event) Msg(message string)            {}

func logger() *event { return &event{} }

var errEmpty = errors.New("empty order")

func Discount(total int) int {
	if total >= 100 {
		return 10
	}
	return 0
}

func Price(order Order) int {
	return order.Total - Discount(order.Total)
}

func Late(now, due time.Time) bool {
	return now.After(due)
}

func Allowed(paid, owner, admin bool) bool {
	return paid && (owner || !admin)
}

func Sum(items []int) int {
	sum := 0
	for _, item := range items {
		sum += item * 2
	}
	return sum
}

func Count(items []int) int {
	count := 0
	for i := 0; i < len(items); i++ {
		count++
	}
	return count
}

func Summarise(paid, owed int) Totals {
	return Totals{Paid: paid, Owed: owed}
}

func Load(id string) (Order, error) {
	if id == "" {
		logger().Str("id", id).Msg("load " + id)
		return Order{}, errEmpty
	}
	return Order{ID: id, Total: 100 - 1}, nil
}

func Save(order *Order, total int) error {
	if total < 0 {
		return fmt.Errorf("save %s: %w", order.ID, errEmpty)
	} else {
		order.Total = total
	}
	return nil
}

func Kind(n int) string {
	switch {
	case n%2 == 0:
		return "even"
	default:
		return "odd"
	}
}

func Close(done chan struct{}) {
	close(done)
}

func Shout(text string, times int) string {
	for i := 0; i < times; i += 1 {
		text += "!"
	}
	return text
}
`

const pythonShop = `import logging
import sys
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from collections.abc import Sequence

log = logging.getLogger(__name__)


class Totals:
    def __init__(self, paid: int, owed: int):
        self.paid = paid
        self.owed = owed


def discount(total: int) -> int:
    if total >= 100:
        return 10
    return 0


def price(order: dict) -> int:
    return order["total"] - discount(order["total"])


def allowed(paid: bool, owner: bool, admin: bool) -> bool:
    return paid and (owner or not admin)


def total(items: "Sequence[int]") -> int:
    s = 0
    for item in items:
        s += item * 2
    return s


def summarise(paid: int, owed: int) -> Totals:
    return Totals(paid=paid, owed=owed)


def load(key: str | None) -> dict:
    if key is None:
        log.info("no key %s", key)
        raise ValueError("no key")
    elif key in ("a", "b"):
        return {}
    else:
        return {"key": key}


def parse(text: str) -> int:
    try:
        return int(text)
    except ValueError as err:
        raise KeyError(text) from err


def kind(n: int) -> str:
    match n % 2:
        case 0:
            return "even"
        case m if m > 1:
            return "big"
        case _:
            return "odd"


def close(client) -> None:
    client.close()


def stamp() -> str:
    if sys.version_info >= (3, 14):
        return "new"
    return "old"
`

const allOperators = ['--operators=+ARGUMENT_EMPTY,+ERROR_CAUSE_REMOVE']

function listing(mutants: ReportedMutant[]): string {
  return mutants
    .map((m) => {
      const inside = m.inside ? ` inside ${m.inside}` : ''
      return `${m.id} ${m.line}:${m.column} ${m.status}${inside}\n  ${JSON.stringify(m.original)} -> ${JSON.stringify(m.replacement)}\n`
    })
    .join('')
}

describe('the mutants of a project with no tests, with each operator', () => {
  it('in Go are the ones in the stored list', async () => {
    const dir = goRepository()
    writeFiles(dir, { 'shop/shop.go': goShop })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', ...allOperators])

    await expect(listing(mutants)).toMatchFileSnapshot('./__snapshots__/go-mutants.txt')
  })

  it('in Python are the ones in the stored list', async () => {
    const dir = pythonRepository()
    writeFiles(dir, { 'shop/order.py': pythonShop })

    const { mutants } = await runMutants(dir, ['--base', 'HEAD', ...allOperators])

    await expect(listing(mutants)).toMatchFileSnapshot('./__snapshots__/python-mutants.txt')
  })
})
