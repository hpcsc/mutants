package operator

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

//go:embed operators
var standard embed.FS

var documentSeparator = regexp.MustCompile(`(?m)^---[ \t]*(#.*)?$`)

type Pack struct {
	language string
	rules    []Rule
	hooks    map[string]Hook
}

func Load(language, repository string) (Pack, error) {
	p := Pack{language: language, hooks: map[string]Hook{}}
	for _, hook := range []Hook{namedValueSwap{}} {
		p.hooks[hook.Operator()] = hook
	}

	standardFiles, err := fs.Glob(standard, path.Join("operators", language, "*.yml"))
	if err != nil {
		return Pack{}, err
	}
	if len(standardFiles) == 0 {
		return Pack{}, fmt.Errorf("mutants has no operators for %s", language)
	}
	rules := map[string]Rule{}
	for _, file := range standardFiles {
		text, err := standard.ReadFile(file)
		if err != nil {
			return Pack{}, err
		}
		if err := p.add(rules, file, string(text)); err != nil {
			return Pack{}, err
		}
	}
	if err := p.checkCatalog(rules); err != nil {
		return Pack{}, err
	}

	folder := filepath.Join(".mutants", "operators", language)
	repositoryFiles, err := filepath.Glob(filepath.Join(repository, folder, "*.y*ml"))
	if err != nil {
		return Pack{}, err
	}
	for _, file := range repositoryFiles {
		text, err := os.ReadFile(file)
		if err != nil {
			return Pack{}, err
		}
		if err := p.add(rules, filepath.Join(folder, filepath.Base(file)), string(text)); err != nil {
			return Pack{}, err
		}
	}

	for _, id := range slices.Sorted(maps.Keys(rules)) {
		p.rules = append(p.rules, rules[id])
	}
	return p, nil
}

func (p Pack) Rules() []Rule {
	return slices.Clone(p.rules)
}

// None is a name for Select that runs no operator. It has no sign, so it also turns off the default
// operators, and "+NAME" after it adds one operator.
const None = "none"

func (p Pack) Select(names []string) (Pack, error) {
	operators := map[string]bool{}
	for _, rule := range p.rules {
		operators[rule.Operator] = true
	}

	chosen := map[string]bool{}
	if !slices.ContainsFunc(names, p.unsigned) {
		for _, rule := range p.rules {
			chosen[rule.ID] = !rule.OffByDefault
		}
	}
	for _, name := range names {
		if name == None {
			continue
		}
		operator := strings.TrimLeft(name, "+-")
		if !operators[operator] {
			return Pack{}, fmt.Errorf("mutants has no operator %s: mutants operators lists the operators", operator)
		}
		for _, rule := range p.rules {
			if rule.Operator == operator {
				chosen[rule.ID] = !strings.HasPrefix(name, "-")
			}
		}
	}

	selected := p
	selected.rules = slices.DeleteFunc(slices.Clone(p.rules), func(rule Rule) bool { return !chosen[rule.ID] })
	return selected, nil
}

// Edits takes each file as a path from root.
func (p Pack) Edits(ctx context.Context, matcher Matcher, root string, files []string) ([]Edit, error) {
	if len(p.rules) == 0 || len(files) == 0 {
		return nil, nil
	}
	matches, err := matcher.Match(ctx, p.rules, files)
	if err != nil {
		return nil, err
	}

	rules := map[string]Rule{}
	for _, rule := range p.rules {
		rules[rule.ID] = rule
	}
	matchesOfFile := map[string][]Match{}
	for _, match := range matches {
		matchesOfFile[match.File] = append(matchesOfFile[match.File], match)
	}

	var edits []Edit
	for _, file := range slices.Sorted(maps.Keys(matchesOfFile)) {
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return nil, err
		}
		hookMatches := map[string][]Match{}
		for _, match := range matchesOfFile[file] {
			rule, found := rules[match.Rule]
			if !found || match.Start < 0 || match.Start > match.End || match.End > len(source) {
				return nil, fmt.Errorf("the match of %s in %s at bytes %d to %d is not in the file", match.Rule, file, match.Start, match.End)
			}
			if _, found := p.hooks[rule.Operator]; found {
				hookMatches[rule.Operator] = append(hookMatches[rule.Operator], match)
				continue
			}
			edits = append(edits, Edit{
				File:        file,
				Operator:    rule.Operator,
				Rule:        rule.ID,
				Start:       match.Start,
				End:         match.End,
				Original:    string(source[match.Start:match.End]),
				Replacement: match.Replacement,
			})
		}
		for _, operator := range slices.Sorted(maps.Keys(hookMatches)) {
			edits = append(edits, p.hooks[operator].Edits(source, hookMatches[operator])...)
		}
	}
	return edits, nil
}

func (p Pack) checkCatalog(standardRules map[string]Rule) error {
	operators := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(standardRules)) {
		operator := standardRules[id].Operator
		if _, found := catalog[operator]; !found {
			return fmt.Errorf("the %s rule %s is for %s, which is not in the catalog of operators", p.language, id, operator)
		}
		operators[operator] = true
	}
	for _, operator := range slices.Sorted(maps.Keys(catalog)) {
		if !operators[operator] {
			return fmt.Errorf("mutants has no %s rule for %s, which is in the catalog of operators", p.language, operator)
		}
	}
	return nil
}

func (p Pack) unsigned(name string) bool {
	return !strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "+")
}

func (p Pack) add(rules map[string]Rule, file, text string) error {
	for _, document := range documentSeparator.Split(text, -1) {
		if strings.TrimSpace(document) == "" {
			continue
		}
		var fields struct {
			ID       string         `yaml:"id"`
			Language string         `yaml:"language"`
			Fix      any            `yaml:"fix"`
			Metadata map[string]any `yaml:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(document), &fields); err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if fields.ID == "" {
			return fmt.Errorf("read %s: a rule has no id", file)
		}
		if !strings.EqualFold(fields.Language, p.language) {
			return fmt.Errorf("read %s: the rule %s is for %q, not for %s", file, fields.ID, fields.Language, p.language)
		}
		operator, _, _ := strings.Cut(fields.ID, "/")
		_, hasHook := p.hooks[operator]
		if fields.Fix == nil && !hasHook {
			return fmt.Errorf("read %s: the rule %s has no fix", file, fields.ID)
		}
		rules[fields.ID] = Rule{
			ID:           fields.ID,
			Operator:     operator,
			File:         file,
			OffByDefault: fields.Metadata["default"] == "off" || catalog[operator] == offByDefault,
			Text:         document,
		}
	}
	return nil
}
