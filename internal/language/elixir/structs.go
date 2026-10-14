package elixir

import (
	"context"
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/hpcsc/mutants/internal/operator"
)

//go:embed structs.yml
var structRules string

// a default that is not a literal can hold a value that the build computes, such as the time of the build
var literal = regexp.MustCompile(`^(nil|true|false|-?[0-9][0-9_]*(\.[0-9_]+)?|:[A-Za-z_][A-Za-z0-9_]*[?!]?|"[^"#\\]*"|\[\]|%\{\})$`)

var foldersWithoutSource = []string{".git", "deps", "_build", "node_modules"}

type structDefaults struct {
	root     string
	matcher  operator.Matcher
	once     sync.Once
	defaults map[string]map[string]string
}

func newStructDefaults(root string, matcher operator.Matcher) *structDefaults {
	return &structDefaults{root: root, matcher: matcher}
}

// fieldsOf gives false for a module with no defstruct in the repository.
func (s *structDefaults) fieldsOf(module string) (map[string]string, bool) {
	s.once.Do(s.read)
	fields, found := s.defaults[module]
	return fields, found
}

func (s *structDefaults) read() {
	s.defaults = map[string]map[string]string{}
	var files []string
	_ = filepath.WalkDir(s.root, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case entry.IsDir() && slices.Contains(foldersWithoutSource, entry.Name()):
			return filepath.SkipDir
		case !entry.IsDir() && strings.HasSuffix(path, ".ex"):
			if relative, err := filepath.Rel(s.root, path); err == nil {
				files = append(files, filepath.ToSlash(relative))
			}
		}
		return nil
	})
	var rules []operator.Rule
	for _, text := range strings.Split(structRules, "\n---\n") {
		rules = append(rules, operator.Rule{Text: text})
	}
	matches, err := s.matcher.Match(context.Background(), rules, files)
	if err != nil {
		return
	}
	byFile := map[string][]operator.Match{}
	for _, match := range matches {
		byFile[match.File] = append(byFile[match.File], match)
	}
	for file, found := range byFile {
		source, err := os.ReadFile(filepath.Join(s.root, file))
		if err != nil {
			continue
		}
		text := func(span operator.Span) string { return string(source[span.Start:span.End]) }
		var modules []module
		for _, match := range found {
			if match.Rule == "module" {
				modules = append(modules, module{name: text(match.Variables["NAME"]), start: match.Start, end: match.End})
			}
		}
		slices.SortFunc(modules, func(a, b module) int { return a.start - b.start })
		for _, match := range found {
			var field, value string
			switch match.Rule {
			case "field-default":
				field, value = keyName(text(match.Variables["KEY"])), compact(text(match.Variables["VALUE"]))
			case "field-without-default":
				field, value = strings.TrimPrefix(text(match.Variables["KEY"]), ":"), "nil"
			default:
				continue
			}
			name := moduleChain(modules, match.Start)
			if name == "" || !literal.MatchString(value) {
				continue
			}
			if s.defaults[name] == nil {
				s.defaults[name] = map[string]string{}
			}
			s.defaults[name][field] = value
		}
	}
}

func moduleChain(modules []module, offset int) string {
	var names []string
	for _, m := range modules {
		if m.holds(offset) {
			names = append(names, m.name)
		}
	}
	return strings.Join(names, ".")
}

func keyName(key string) string {
	return strings.TrimSuffix(strings.TrimSpace(key), ":")
}

func compact(text string) string {
	return strings.Join(strings.Fields(text), "")
}
