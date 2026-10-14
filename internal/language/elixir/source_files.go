package elixir

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/hpcsc/mutants/internal/operator"
)

//go:embed declarations.yml
var declarationRules string

type module struct {
	name       string
	start, end int
}

func (m module) holds(offset int) bool {
	return m.start <= offset && offset < m.end
}

type function struct {
	name       string
	arity      int
	head       operator.Span
	signature  operator.Span
	start, end int
	lines      lineRange
}

func (f function) holds(offset int) bool {
	return f.start <= offset && offset < f.end
}

type spec struct {
	name    string
	arity   int
	returns string
	head    operator.Span
	offset  int
}

type binding struct {
	name          string
	offset        int
	kind          string
	functionIndex int
}

// clauseHead holds in lines its case, cond, receive, with or fn, or each clause of its function for a function head.
type clauseHead struct {
	start, end   int
	lines        []lineRange
	functionHead bool
}

type structField struct {
	start, end int
	structName string
	field      string
	value      string
}

type parsedFile struct {
	modules      []module
	functions    []function
	specs        []spec
	bindings     []binding
	clauseHeads  []clauseHead
	structFields []structField
	aliases      map[string]string
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

func (s *sourceFiles) function(file string, offset int) string {
	parsed := s.parse(file)
	var names []string
	if chain := moduleChain(parsed.modules, offset); chain != "" {
		names = append(names, chain)
	}
	if index := parsed.functionOf(offset); index >= 0 {
		f := parsed.functions[index]
		names = append(names, fmt.Sprintf("%s/%d", f.name, f.arity))
	}
	return strings.Join(names, ".")
}

// removedStructField gives the field of a struct literal that the edit from start to end removes, and the
// modules that the name of its struct can mean, the most likely one first.
func (s *sourceFiles) removedStructField(file string, start, end int) (modules []string, field structField, found bool) {
	parsed := s.parse(file)
	index := slices.IndexFunc(parsed.structFields, func(f structField) bool { return start <= f.start && f.end <= end })
	if index < 0 {
		return nil, structField{}, false
	}
	field = parsed.structFields[index]
	chain := moduleChain(parsed.modules, field.start)
	if field.structName == "__MODULE__" {
		return []string{chain}, field, true
	}
	first, rest, nested := strings.Cut(field.structName, ".")
	if full, aliased := parsed.aliases[first]; aliased {
		if nested {
			full += "." + rest
		}
		return []string{full}, field, true
	}
	for prefix := chain; prefix != ""; prefix = prefix[:max(0, strings.LastIndex(prefix, "."))] {
		modules = append(modules, prefix+"."+field.structName)
	}
	return append(modules, field.structName), field, true
}

// returnType gives the name of the function that holds the offset, and the return type of its @spec.
func (s *sourceFiles) returnType(file string, offset int) (name, returns string) {
	parsed := s.parse(file)
	index := parsed.functionOf(offset)
	if index < 0 {
		return "", ""
	}
	f := parsed.functions[index]
	module := parsed.moduleOf(f.start)
	for _, sp := range parsed.specs {
		if sp.name == f.name && sp.arity == f.arity && parsed.moduleOf(sp.offset) == module {
			return f.name, sp.returns
		}
	}
	return f.name, ""
}

// a read of a variable after a mutant of its only binding does not compile
func (s *sourceFiles) unbound(file string, start, end int) []string {
	parsed := s.parse(file)
	var names []string
	for _, target := range parsed.bindings {
		if target.kind != "assigned" || target.offset < start || target.offset >= end {
			continue
		}
		bound := slices.ContainsFunc(parsed.bindings, func(b binding) bool {
			return b.name == target.name && b.functionIndex == target.functionIndex && (b.kind == "parameter" || b.offset < start)
		})
		if !bound {
			names = append(names, target.name)
		}
	}
	return names
}

// functionLines gives nil for an offset outside a function.
func (s *sourceFiles) functionLines(file string, offset int) []lineRange {
	parsed := s.parse(file)
	if index := parsed.functionOf(offset); index >= 0 {
		return []lineRange{parsed.functions[index].lines}
	}
	return nil
}

func (s *sourceFiles) clauseHeadOf(file string, offset int) (clauseHead, bool) {
	var found clauseHead
	for _, head := range s.parse(file).clauseHeads {
		if head.start <= offset && offset < head.end {
			found = head
		}
	}
	return found, found.lines != nil
}

func (p parsedFile) functionOf(offset int) int {
	found := -1
	for i, f := range p.functions {
		if f.holds(offset) {
			found = i
		}
	}
	return found
}

func (p parsedFile) moduleOf(offset int) int {
	found := -1
	for i, m := range p.modules {
		if m.holds(offset) {
			found = i
		}
	}
	return found
}

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

	lineOf := func(offset int) int { return strings.Count(string(source[:offset]), "\n") + 1 }
	linesOf := func(start, end int) lineRange {
		return lineRange{first: lineOf(start), last: lineOf(max(start, end-1))}
	}

	parsed := parsedFile{aliases: map[string]string{}}
	var arguments []int
	for _, match := range matches {
		name := text(match.Variables["NAME"])
		switch match.Rule {
		case "module":
			parsed.modules = append(parsed.modules, module{name: name, start: match.Start, end: match.End})
		case "implementation":
			if implemented, found := match.Variables["FOR"]; found {
				name += "." + text(implemented)
			}
			parsed.modules = append(parsed.modules, module{name: name, start: match.Start, end: match.End})
		case "function":
			parsed.functions = append(parsed.functions, function{
				name: name, head: match.Variables["HEAD"], signature: match.Variables["SIGNATURE"], start: match.Start, end: match.End, lines: linesOf(match.Start, match.End),
			})
		case "clause-head":
			construct := match.Variables["CONSTRUCT"]
			parsed.clauseHeads = append(parsed.clauseHeads, clauseHead{start: match.Start, end: match.End, lines: []lineRange{linesOf(construct.Start, construct.End)}})
		case "spec":
			parsed.specs = append(parsed.specs, spec{name: name, returns: text(match.Variables["RETURNS"]), head: match.Variables["HEAD"], offset: match.Start})
		case "argument":
			arguments = append(arguments, match.Start)
		case "parameter", "assigned", "bound":
			parsed.bindings = append(parsed.bindings, binding{name: name, offset: match.Start, kind: match.Rule})
		case "struct-field":
			parsed.structFields = append(parsed.structFields, structField{
				start: match.Start, end: match.End, structName: text(match.Variables["STRUCT"]),
				field: keyName(text(match.Variables["KEY"])), value: compact(text(match.Variables["VALUE"])),
			})
		case "alias":
			short := name[strings.LastIndex(name, ".")+1:]
			if as, found := match.Variables["AS"]; found {
				short = text(as)
			}
			parsed.aliases[short] = name
		}
	}
	countIn := func(head operator.Span) int {
		return len(slices.DeleteFunc(slices.Clone(arguments), func(offset int) bool { return offset < head.Start || offset >= head.End }))
	}
	for i := range parsed.functions {
		parsed.functions[i].arity = countIn(parsed.functions[i].head)
	}
	for i := range parsed.specs {
		parsed.specs[i].arity = countIn(parsed.specs[i].head)
	}
	slices.SortFunc(parsed.modules, func(a, b module) int { return a.start - b.start })
	slices.SortFunc(parsed.functions, func(a, b function) int { return a.start - b.start })
	for i := range parsed.bindings {
		parsed.bindings[i].functionIndex = parsed.functionOf(parsed.bindings[i].offset)
	}
	for _, f := range parsed.functions {
		head := clauseHead{start: f.signature.Start, end: f.signature.End, functionHead: true}
		for _, clause := range parsed.functions {
			if clause.name == f.name && clause.arity == f.arity && parsed.moduleOf(clause.start) == parsed.moduleOf(f.start) {
				head.lines = append(head.lines, clause.lines)
			}
		}
		parsed.clauseHeads = append(parsed.clauseHeads, head)
	}
	slices.SortFunc(parsed.clauseHeads, func(a, b clauseHead) int { return a.start - b.start })
	s.files[file] = parsed
	return parsed
}
