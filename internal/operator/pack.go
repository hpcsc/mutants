package operator

import (
	"bytes"
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
	"unicode"

	"go.yaml.in/yaml/v3"
)

//go:embed operators skip
var standard embed.FS

var documentSeparator = regexp.MustCompile(`(?m)^---[ \t]*(#.*)?$`)

type Pack struct {
	language string
	rules    []Rule
	skips    []Rule
	hooks    map[string]Hook
}

func Load(language, repository string) (Pack, error) {
	p := Pack{language: language, hooks: map[string]Hook{}}
	for _, hook := range []Hook{namedValueSwap{}} {
		p.hooks[hook.Operator()] = hook
	}

	rules := map[string]Rule{}
	found, err := readStandard(path.Join("operators", language), rules, p.add)
	if err != nil {
		return Pack{}, err
	}
	if found == 0 {
		return Pack{}, fmt.Errorf("mutants has no operators for %s", language)
	}
	if err := p.checkCatalog(rules); err != nil {
		return Pack{}, err
	}
	if err := readRepository(repository, filepath.Join(".mutants", "operators", language), rules, p.add); err != nil {
		return Pack{}, err
	}
	for _, id := range slices.Sorted(maps.Keys(rules)) {
		p.rules = append(p.rules, rules[id])
	}

	skips := map[string]Rule{}
	if _, err := readStandard(path.Join("skip", language), skips, p.addSkip); err != nil {
		return Pack{}, err
	}
	if err := readRepository(repository, filepath.Join(".mutants", "skip", language), skips, p.addSkip); err != nil {
		return Pack{}, err
	}
	for _, id := range slices.Sorted(maps.Keys(skips)) {
		if _, found := rules[id]; found {
			return Pack{}, fmt.Errorf("the skip rule %s has the id of a rule of an operator", id)
		}
		p.skips = append(p.skips, skips[id])
	}
	return p, nil
}

type addRules func(rules map[string]Rule, file, text string) error

func readStandard(folder string, rules map[string]Rule, add addRules) (int, error) {
	files, err := fs.Glob(standard, path.Join(folder, "*.yml"))
	if err != nil {
		return 0, err
	}
	for _, file := range files {
		text, err := standard.ReadFile(file)
		if err != nil {
			return 0, err
		}
		if err := add(rules, file, string(text)); err != nil {
			return 0, err
		}
	}
	return len(files), nil
}

func readRepository(repository, folder string, rules map[string]Rule, add addRules) error {
	files, err := filepath.Glob(filepath.Join(repository, folder, "*.y*ml"))
	if err != nil {
		return err
	}
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := add(rules, filepath.Join(folder, filepath.Base(file)), string(text)); err != nil {
			return err
		}
	}
	return nil
}

func (p Pack) Rules() []Rule {
	return slices.Clone(p.rules)
}

// None is a name for Select that runs no operator. It has no sign, so it also turns off the default
// operators, and "+NAME" after it adds one operator.
const None = "none"

func Select(packs []Pack, names []string) ([]Pack, error) {
	known := map[string]bool{}
	for _, p := range packs {
		for _, rule := range p.rules {
			known[rule.Operator] = true
		}
	}
	for _, name := range names {
		if operator := strings.TrimLeft(name, "+-"); name != None && !known[operator] {
			return nil, fmt.Errorf("mutants has no operator %s: mutants operators lists the operators", operator)
		}
	}
	selected := make([]Pack, 0, len(packs))
	for _, p := range packs {
		var own []string
		for _, name := range names {
			if name == None || p.has(strings.TrimLeft(name, "+-")) {
				own = append(own, name)
			}
		}
		if slices.ContainsFunc(names, p.unsigned) && !slices.ContainsFunc(own, p.unsigned) {
			own = append(own, None)
		}
		chosen, err := p.Select(own)
		if err != nil {
			return nil, err
		}
		selected = append(selected, chosen)
	}
	return selected, nil
}

func (p Pack) has(operator string) bool {
	return slices.ContainsFunc(p.rules, func(rule Rule) bool { return rule.Operator == operator })
}

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
	matches, err := matcher.Match(ctx, append(slices.Clone(p.rules), p.skips...), files)
	if err != nil {
		return nil, err
	}

	rules := map[string]Rule{}
	for _, rule := range p.rules {
		rules[rule.ID] = rule
	}
	skips := map[string]bool{}
	for _, skip := range p.skips {
		skips[skip.ID] = true
	}
	matchesOfFile := map[string][]Match{}
	skippedOfFile := map[string][]Span{}
	for _, match := range matches {
		if skips[match.Rule] {
			skippedOfFile[match.File] = append(skippedOfFile[match.File], Span{Start: match.Start, End: match.End})
			continue
		}
		matchesOfFile[match.File] = append(matchesOfFile[match.File], match)
	}

	var edits []Edit
	for _, file := range slices.Sorted(maps.Keys(matchesOfFile)) {
		source, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return nil, err
		}
		var fileEdits []Edit
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
			fileEdits = append(fileEdits, Edit{
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
			fileEdits = append(fileEdits, p.hooks[operator].Edits(source, hookMatches[operator])...)
		}
		edits = append(edits, slices.DeleteFunc(fileEdits, func(edit Edit) bool { return skipped(edit, skippedOfFile[file]) })...)
	}
	return edits, nil
}

func skipped(edit Edit, skippedCode []Span) bool {
	rest := []byte(edit.Original)
	changesSkippedCode := false
	for _, span := range skippedCode {
		if span.Start <= edit.Start && edit.End <= span.End {
			return true
		}
		if edit.Start <= span.Start && span.End <= edit.End {
			changesSkippedCode = true
			for i := span.Start; i < span.End; i++ {
				rest[i-edit.Start] = ' '
			}
		}
	}
	// the braces and separators around the skipped code have no letter and no digit
	return changesSkippedCode && !bytes.ContainsFunc(rest, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
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
	parsedRules, err := p.parse(file, text)
	if err != nil {
		return err
	}
	for _, parsed := range parsedRules {
		operator, _, _ := strings.Cut(parsed.id, "/")
		_, hasHook := p.hooks[operator]
		if !parsed.hasFix && !hasHook {
			return fmt.Errorf("read %s: the rule %s has no fix", file, parsed.id)
		}
		rules[parsed.id] = Rule{
			ID:           parsed.id,
			Operator:     operator,
			File:         file,
			OffByDefault: parsed.metadata["default"] == "off" || catalog[operator] == offByDefault,
			Text:         parsed.text,
		}
	}
	return nil
}

func (p Pack) addSkip(skips map[string]Rule, file, text string) error {
	parsedRules, err := p.parse(file, text)
	if err != nil {
		return err
	}
	for _, parsed := range parsedRules {
		if parsed.hasFix {
			return fmt.Errorf("read %s: the skip rule %s has a fix, but a skip rule changes no code", file, parsed.id)
		}
		skips[parsed.id] = Rule{ID: parsed.id, File: file, Text: parsed.text}
	}
	return nil
}

type parsedRule struct {
	id       string
	hasFix   bool
	metadata map[string]any
	text     string
}

func (p Pack) parse(file, text string) ([]parsedRule, error) {
	var parsedRules []parsedRule
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
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		if fields.ID == "" {
			return nil, fmt.Errorf("read %s: a rule has no id", file)
		}
		if !strings.EqualFold(fields.Language, p.language) {
			return nil, fmt.Errorf("read %s: the rule %s is for %q, not for %s", file, fields.ID, fields.Language, p.language)
		}
		parsedRules = append(parsedRules, parsedRule{id: fields.ID, hasFix: fields.Fix != nil, metadata: fields.Metadata, text: document})
	}
	return parsedRules, nil
}
