package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/hpcsc/mutants/internal/language/python"
	"github.com/urfave/cli/v3"
)

func newOperatorsCommand() *cli.Command {
	return &cli.Command{
		Name:   "operators",
		Usage:  "list the operators and their rules",
		Action: listOperators,
	}
}

func listOperators(ctx context.Context, cmd *cli.Command) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if repository, err := diff.Open(ctx, root); err == nil {
		root = repository.Root()
	}
	languages, err := newLanguages(root, golang.Settings{}, python.Settings{})
	if err != nil {
		return err
	}

	type listed struct {
		language string
		name     string
		runs     string
		rules    []string
		sources  []string
	}
	var operators []*listed
	for _, l := range languages {
		byName := map[string]*listed{}
		for _, rule := range l.Pack.Rules() {
			entry, found := byName[rule.Operator]
			if !found {
				entry = &listed{language: l.Adapter.Name(), name: rule.Operator, runs: "off"}
				byName[rule.Operator] = entry
				operators = append(operators, entry)
			}
			if !rule.OffByDefault {
				entry.runs = "on"
			}
			entry.rules = append(entry.rules, rule.ID)
			if strings.HasPrefix(rule.File, ".mutants") {
				entry.sources = append(entry.sources, rule.File)
			}
		}
	}

	table := tabwriter.NewWriter(cmd.Root().Writer, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "LANGUAGE\tOPERATOR\tBY DEFAULT\tRULES")
	for _, entry := range operators {
		rules := strings.Join(entry.rules, " ")
		if len(entry.sources) > 0 {
			rules += " (from " + strings.Join(entry.sources, ", ") + ")"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", entry.language, entry.name, entry.runs, rules)
	}
	return table.Flush()
}
