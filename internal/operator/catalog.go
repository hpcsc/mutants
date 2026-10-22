package operator

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type Tier int

const (
	Required Tier = iota
	Optional
)

// LeftOutLimit is the count of languages that leave out a required operator at which the operator must become
// optional.
const LeftOutLimit = 2

type Entry struct {
	Tier         Tier
	OffByDefault bool
	// LeftOut maps each language that has no such code, and so no rule for a required operator, to the reason.
	LeftOut map[string]string
}

type Catalog map[string]Entry

var catalog = Catalog{
	"ARGUMENT_EMPTY":        {Tier: Required, OffByDefault: true},
	"ARITHMETIC_BASE":       {Tier: Required},
	"BRANCH_CASE":           {Tier: Required},
	"BRANCH_ELSE":           {Tier: Required},
	"BRANCH_IF":             {Tier: Required},
	"BREAK_AT_END":          {Tier: Required},
	"BREAK_AT_START":        {Tier: Required},
	"CONDITIONALS_BOUNDARY": {Tier: Required},
	"CONDITIONALS_NEGATION": {Tier: Required},
	"CONDITION_TRUE":        {Tier: Required, OffByDefault: true},
	"ERROR_CAUSE_REMOVE":    {Tier: Required, OffByDefault: true},
	"ERROR_REMOVE":          {Tier: Required},
	"EXPRESSION_REMOVE":     {Tier: Required},
	"GUARD_REMOVE":          {Tier: Optional},
	"INCREMENT_DECREMENT":   {Tier: Required},
	"INTEGER_DECREMENT":     {Tier: Required},
	"INTEGER_INCREMENT":     {Tier: Required},
	"INVERT_CALL":           {Tier: Required},
	"INVERT_LOGICAL":        {Tier: Required},
	"INVERT_NEGATIVES":      {Tier: Required},
	"NAMED_VALUE_REMOVE":    {Tier: Required},
	"NAMED_VALUE_SWAP":      {Tier: Required},
	"REMOVE_LOGICAL_NOT":    {Tier: Required},
	"RETURN_EMPTY":          {Tier: Required},
	"RETURN_ERROR":          {Tier: Optional},
	"RETURN_TRUE":           {Tier: Required},
	"STATEMENT_REMOVE":      {Tier: Required},
}

func Standard() Catalog {
	return maps.Clone(catalog)
}

// Check takes the standard rules of one language.
func (c Catalog) Check(language string, rules []Rule) error {
	sorted := slices.SortedFunc(slices.Values(rules), func(a, b Rule) int { return strings.Compare(a.ID, b.ID) })
	operators := map[string]bool{}
	for _, rule := range sorted {
		entry, found := c[rule.Operator]
		if !found {
			return fmt.Errorf("the %s rule %s is for %s, which is not in the catalog of operators", language, rule.ID, rule.Operator)
		}
		if reason, leftOut := entry.LeftOut[language]; leftOut {
			return fmt.Errorf("the %s rule %s is for %s, which the catalog leaves out of %s: %s", language, rule.ID, rule.Operator, language, reason)
		}
		operators[rule.Operator] = true
	}
	for _, name := range slices.Sorted(maps.Keys(c)) {
		entry := c[name]
		if _, leftOut := entry.LeftOut[language]; entry.Tier == Required && !leftOut && !operators[name] {
			return fmt.Errorf("mutants has no %s rule for %s, which is a required operator of the catalog", language, name)
		}
	}
	return nil
}

func (c Catalog) CheckTiers() error {
	for _, name := range slices.Sorted(maps.Keys(c)) {
		entry := c[name]
		switch {
		case entry.Tier == Optional && len(entry.LeftOut) > 0:
			return fmt.Errorf("%s is optional, so no language leaves it out: a language without its code gives no rule", name)
		case entry.Tier == Required && len(entry.LeftOut) >= LeftOutLimit:
			return fmt.Errorf("%s is required, but %d languages leave it out: make it optional", name, len(entry.LeftOut))
		}
	}
	return nil
}
