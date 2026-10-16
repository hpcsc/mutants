//go:build unit

package operator_test

import (
	"testing"

	"github.com/hpcsc/mutants/internal/operator"
	"github.com/stretchr/testify/require"
)

func TestCatalog(t *testing.T) {
	t.Run("standard", func(t *testing.T) {
		t.Run("gives the operators of mutants in a copy, so that a change to it changes no other catalog", func(t *testing.T) {
			changed := operator.Standard()
			changed["COUNT_ZERO"] = operator.Entry{Tier: operator.Optional}

			standard := operator.Standard()

			require.Contains(t, standard, "BRANCH_IF")
			require.NotContains(t, standard, "COUNT_ZERO")
		})
	})

	t.Run("check", func(t *testing.T) {
		t.Run("a pack with a rule for each required operator passes", func(t *testing.T) {
			catalog := operator.Catalog{"BRANCH_IF": {Tier: operator.Required}, "BRANCH_CASE": {Tier: operator.Required}}

			err := catalog.Check("go", []operator.Rule{{ID: "BRANCH_IF", Operator: "BRANCH_IF"}, {ID: "BRANCH_CASE", Operator: "BRANCH_CASE"}})

			require.NoError(t, err)
		})

		t.Run("a required operator with no rule returns an error that names it, also after an operator that has a rule", func(t *testing.T) {
			catalog := operator.Catalog{"BRANCH_IF": {Tier: operator.Required}, "STATEMENT_REMOVE": {Tier: operator.Required}}

			err := catalog.Check("go", []operator.Rule{{ID: "BRANCH_IF", Operator: "BRANCH_IF"}})

			require.EqualError(t, err, "mutants has no go rule for STATEMENT_REMOVE, which is a required operator of the catalog")
		})

		t.Run("a required operator that the catalog leaves out of the language needs no rule", func(t *testing.T) {
			catalog := operator.Catalog{
				"BRANCH_IF":   {Tier: operator.Required},
				"BRANCH_CASE": {Tier: operator.Required, LeftOut: map[string]string{"hcl": "HCL has no switch"}},
			}

			err := catalog.Check("hcl", []operator.Rule{{ID: "BRANCH_IF", Operator: "BRANCH_IF"}})

			require.NoError(t, err)
		})

		t.Run("a rule for an operator that the catalog leaves out of the language returns an error with the reason", func(t *testing.T) {
			catalog := operator.Catalog{"BRANCH_CASE": {Tier: operator.Required, LeftOut: map[string]string{"hcl": "HCL has no switch"}}}

			err := catalog.Check("hcl", []operator.Rule{{ID: "BRANCH_CASE/when", Operator: "BRANCH_CASE"}})

			require.EqualError(t, err, "the hcl rule BRANCH_CASE/when is for BRANCH_CASE, which the catalog leaves out of hcl: HCL has no switch")
		})

		t.Run("an optional operator needs no rule, and can have one", func(t *testing.T) {
			catalog := operator.Catalog{"BRANCH_IF": {Tier: operator.Required}, "COUNT_ZERO": {Tier: operator.Optional}}

			withoutRule := catalog.Check("go", []operator.Rule{{ID: "BRANCH_IF", Operator: "BRANCH_IF"}})
			withRule := catalog.Check("hcl", []operator.Rule{{ID: "BRANCH_IF", Operator: "BRANCH_IF"}, {ID: "COUNT_ZERO", Operator: "COUNT_ZERO"}})

			require.NoError(t, withoutRule)
			require.NoError(t, withRule)
		})

		t.Run("a rule for an operator that is not in the catalog returns an error that names the rule", func(t *testing.T) {
			catalog := operator.Catalog{"BRANCH_IF": {Tier: operator.Required}}

			err := catalog.Check("go", []operator.Rule{{ID: "BRANCH_IF", Operator: "BRANCH_IF"}, {ID: "NIL_MAP/empty", Operator: "NIL_MAP"}})

			require.EqualError(t, err, "the go rule NIL_MAP/empty is for NIL_MAP, which is not in the catalog of operators")
		})

		t.Run("with two rules that the catalog refuses, the error names the first rule by its id", func(t *testing.T) {
			catalog := operator.Catalog{"BRANCH_IF": {Tier: operator.Required}}

			err := catalog.Check("go", []operator.Rule{{ID: "BRANCH_IF", Operator: "BRANCH_IF"}, {ID: "ZERO_MAP", Operator: "ZERO_MAP"}, {ID: "NIL_MAP", Operator: "NIL_MAP"}})

			require.EqualError(t, err, "the go rule NIL_MAP is for NIL_MAP, which is not in the catalog of operators")
		})
	})

	t.Run("check tiers", func(t *testing.T) {
		t.Run("the standard catalog has no required operator that two languages leave out", func(t *testing.T) {
			require.NoError(t, operator.Standard().CheckTiers())
		})

		t.Run("a required operator that one language leaves out, and an optional operator, pass", func(t *testing.T) {
			catalog := operator.Catalog{
				"BRANCH_CASE": {Tier: operator.Required, LeftOut: map[string]string{"hcl": "HCL has no switch"}},
				"COUNT_ZERO":  {Tier: operator.Optional},
			}

			require.NoError(t, catalog.CheckTiers())
		})

		t.Run("a required operator that two languages leave out returns an error that says to make it optional, also after an operator that passes", func(t *testing.T) {
			catalog := operator.Catalog{
				"ARITHMETIC_BASE": {Tier: operator.Required},
				"BRANCH_CASE":     {Tier: operator.Required, LeftOut: map[string]string{"hcl": "HCL has no switch", "yaml": "YAML has no switch"}},
			}

			require.EqualError(t, catalog.CheckTiers(), "BRANCH_CASE is required, but 2 languages leave it out: make it optional")
		})

		t.Run("an optional operator that a language leaves out returns an error, because a language without its code gives no rule", func(t *testing.T) {
			catalog := operator.Catalog{"COUNT_ZERO": {Tier: operator.Optional, LeftOut: map[string]string{"go": "Go has no count"}}}

			require.EqualError(t, catalog.CheckTiers(), "COUNT_ZERO is optional, so no language leaves it out: a language without its code gives no rule")
		})
	})
}
