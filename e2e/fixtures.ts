export const figures = `package figures

type Totals struct {
	Paid, Owed int
}

func Summarise(paid, owed int) Totals {
	return Totals{Paid: paid, Owed: owed}
}
`

export function figuresTest(paid: number, owed: number): string {
  return `package figures

import "testing"

func TestSummarise(t *testing.T) {
	if got := Summarise(${paid}, ${owed}); got != (Totals{Paid: ${paid}, Owed: ${owed}}) {
		t.Fatalf("got %v", got)
	}
}
`
}

export const accounts = `package accounts

import "fmt"

func Load(id int) (int, error) {
	if id < 0 {
		return 0, fmt.Errorf("load the account %d: the id is negative", id)
	}
	return id, nil
}
`

export const accountsTest = `package accounts

import "testing"

func TestLoad(t *testing.T) {
	if id, err := Load(1); err != nil || id != 1 {
		t.Fatalf("got %d, %v", id, err)
	}
}
`

export const total = `package total

func Sum(items []int) int {
	sum := 0
	for _, item := range items {
		sum += item
	}
	return sum
}
`

export function totalTest(items: number[]): string {
  const sum = items.reduce((a, b) => a + b, 0)
  return `package total

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]int{${items.join(', ')}}); got != ${sum} {
		t.Fatalf("got %d", got)
	}
}
`
}

export const maxSource = `package calc

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
`

export const maxTest = `package calc

import "testing"

func TestMax(t *testing.T) {
	if Max(1, 2) != 2 || Max(2, 1) != 2 {
		t.Fatal("Max gives the wrong number")
	}
}
`
