package cmd

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language/elixir"
	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/hpcsc/mutants/internal/language/python"
	"github.com/hpcsc/mutants/internal/operator"
	"github.com/hpcsc/mutants/internal/run"
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
	sharedGitFolder := ""
	if repository, err := diff.Open(ctx, root); err == nil {
		root = repository.Root()
		sharedGitFolder, _ = repository.SharedGitFolder(ctx)
	}
	languages, err := newLanguages(root, sharedGitFolder, golang.Settings{}, python.Settings{}, elixir.Settings{}, cmd.Root().ErrWriter)
	if err != nil {
		return err
	}
	return writeOperators(cmd.Root().Writer, languages, operator.Standard())
}

func writeOperators(w io.Writer, languages []run.Language, catalog operator.Catalog) error {
	type listed struct {
		language string
		name     string
		runs     string
		rules    []string
		sources  []string
		leftOut  string
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
			if !rule.IsStandard() {
				entry.sources = append(entry.sources, rule.File)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(catalog)) {
			if reason, leftOut := catalog[name].LeftOut[l.Adapter.Name()]; leftOut {
				operators = append(operators, &listed{language: l.Adapter.Name(), name: name, runs: "-", leftOut: reason})
			}
		}
	}

	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "LANGUAGE\tOPERATOR\tBY DEFAULT\tRULES")
	for _, entry := range operators {
		rules := strings.Join(entry.rules, " ")
		if len(entry.sources) > 0 {
			rules += " (from " + strings.Join(entry.sources, ", ") + ")"
		}
		if entry.leftOut != "" {
			rules = "none: " + entry.leftOut
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", entry.language, entry.name, entry.runs, rules)
	}
	return table.Flush()
}
