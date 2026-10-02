package python

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/hpcsc/mutants/internal/operator"
)

//go:embed declarations.yml
var declarationRules string

type declaration struct {
	name       string
	function   bool
	returns    string
	start, end int
}

func (d declaration) holds(offset int) bool {
	return d.start <= offset && offset < d.end
}

// binding is a place where a name gets a value. Its kind is the id of the rule in declarations.yml that
// found it, and function is the index of the innermost function that holds it.
type binding struct {
	name     string
	offset   int
	kind     string
	function int
}

type parsedFile struct {
	declarations []declaration
	bindings     []binding
}

type sourceFiles struct {
	root    string
	matcher operator.Matcher
	mutex   sync.Mutex
	files   map[string]parsedFile
}

func newSourceFiles(root string, matcher operator.Matcher) *sourceFiles {
	return &sourceFiles{root: root, matcher: matcher, files: map[string]parsedFile{}}
}

// function gives the names of the classes and the functions that hold the offset, from the outside in, such
// as Cart.total.
func (s *sourceFiles) function(file string, offset int) string {
	var names []string
	for _, d := range s.parse(file).declarations {
		if d.holds(offset) {
			names = append(names, d.name)
		}
	}
	return strings.Join(names, ".")
}

func (s *sourceFiles) returnType(file string, offset int) string {
	parsed := s.parse(file)
	if index := parsed.functionOf(offset); index >= 0 {
		return parsed.declarations[index].returns
	}
	return ""
}

// unbound gives the names that an assignment from start to end assigns, and that have no value before it in
// its function. A read of such a name after a mutant of the assignment raises NameError.
func (s *sourceFiles) unbound(file string, start, end int) []string {
	parsed := s.parse(file)
	var names []string
	for _, target := range parsed.bindings {
		if target.kind != "assigned" || target.offset < start || target.offset >= end {
			continue
		}
		bound := slices.ContainsFunc(parsed.bindings, func(b binding) bool {
			return b.name == target.name && b.function == target.function && (b.kind == "parameter" || b.offset < start)
		})
		if !bound {
			names = append(names, target.name)
		}
	}
	return names
}

// functionOf gives the index of the innermost function that holds the offset, or -1.
func (p parsedFile) functionOf(offset int) int {
	found := -1
	for i, d := range p.declarations {
		if d.function && d.holds(offset) {
			found = i
		}
	}
	return found
}

// parse gives the classes and the functions of the file in the order of their start, so a declaration comes
// before each declaration in it.
func (s *sourceFiles) parse(file string) parsedFile {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if found, read := s.files[file]; read {
		return found
	}
	var rules []operator.Rule
	for _, text := range strings.Split(declarationRules, "\n---\n") {
		rules = append(rules, operator.Rule{Text: text})
	}
	matches, err := s.matcher.Match(context.Background(), rules, []string{file})
	source, readErr := os.ReadFile(filepath.Join(s.root, file))
	if err != nil || readErr != nil {
		s.files[file] = parsedFile{}
		return parsedFile{}
	}
	text := func(span operator.Span) string { return string(source[span.Start:span.End]) }
	byRange := map[[2]int]*declaration{}
	var found []*declaration
	var bindings []binding
	for _, match := range matches {
		switch match.Rule {
		case "parameter", "assigned", "bound":
			bindings = append(bindings, binding{name: text(match.Variables["NAME"]), offset: match.Start, kind: match.Rule})
			continue
		}
		key := [2]int{match.Start, match.End}
		d, seen := byRange[key]
		if !seen {
			d = &declaration{start: match.Start, end: match.End}
			byRange[key] = d
			found = append(found, d)
		}
		switch match.Rule {
		case "function", "class":
			d.name, d.function = text(match.Variables["NAME"]), match.Rule == "function"
		case "returns":
			d.returns = text(match.Variables["RETURNS"])
		}
	}
	parsed := parsedFile{declarations: make([]declaration, 0, len(found))}
	for _, d := range found {
		parsed.declarations = append(parsed.declarations, *d)
	}
	slices.SortFunc(parsed.declarations, func(a, b declaration) int { return a.start - b.start })
	for _, b := range bindings {
		b.function = parsed.functionOf(b.offset)
		parsed.bindings = append(parsed.bindings, b)
	}
	s.files[file] = parsed
	return parsed
}
