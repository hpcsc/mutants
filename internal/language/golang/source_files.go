package golang

import (
	"cmp"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/tools/go/ast/astutil"
)

type sourceFiles struct {
	root  string
	mutex sync.Mutex
	files map[string]parsedFile
}

type parsedFile struct {
	syntax *ast.File
	lines  *token.File
	err    error
}

func newSourceFiles(root string) *sourceFiles {
	return &sourceFiles{root: root, files: map[string]parsedFile{}}
}

func (s *sourceFiles) parse(file string) (*ast.File, *token.File, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	parsed, found := s.files[file]
	if !found {
		positions := token.NewFileSet()
		parsed.syntax, parsed.err = parser.ParseFile(positions, filepath.Join(s.root, file), nil, parser.ParseComments|parser.SkipObjectResolution)
		if parsed.err == nil {
			parsed.lines = positions.File(parsed.syntax.Pos())
		}
		s.files[file] = parsed
	}
	return parsed.syntax, parsed.lines, parsed.err
}

func (s *sourceFiles) function(file string, offset int) string {
	syntax, lines, err := s.parse(file)
	if err != nil || offset < 0 || offset > lines.Size() {
		return ""
	}
	position := lines.Pos(offset)
	for _, declaration := range syntax.Decls {
		if position < declaration.Pos() || position >= declaration.End() {
			continue
		}
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			return s.funcName(declaration)
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				if position < spec.Pos() || position >= spec.End() {
					continue
				}
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					return spec.Names[0].Name
				case *ast.TypeSpec:
					return spec.Name.Name
				}
			}
		}
	}
	return ""
}

// the compiler refuses a decrement of a constant 0 in an index, a slice bound or a size
func (s *sourceFiles) isZeroIndexOrSize(file string, start, end int) bool {
	syntax, lines, err := s.parse(file)
	if err != nil || start < 0 || end > lines.Size() {
		return false
	}
	enclosing, _ := astutil.PathEnclosingInterval(syntax, lines.Pos(start), lines.Pos(end))
	if len(enclosing) < 2 {
		return false
	}
	literal, ok := enclosing[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.INT || literal.Value != "0" {
		return false
	}
	switch parent := enclosing[1].(type) {
	case *ast.IndexExpr:
		return parent.Index == literal
	case *ast.SliceExpr:
		return parent.Low == literal || parent.High == literal || parent.Max == literal
	case *ast.CallExpr:
		function, isIdentifier := parent.Fun.(*ast.Ident)
		return isIdentifier && function.Name == "make" && len(parent.Args) > 1 && parent.Args[0] != literal
	}
	return false
}

func (s *sourceFiles) funcName(declaration *ast.FuncDecl) string {
	if declaration.Recv == nil || len(declaration.Recv.List) == 0 {
		return declaration.Name.Name
	}
	receiver := declaration.Recv.List[0].Type
	name := cmp.Or(typeName(receiver), "?")
	if _, pointer := receiver.(*ast.StarExpr); pointer {
		return "(*" + name + ")." + declaration.Name.Name
	}
	return name + "." + declaration.Name.Name
}

// belongsTo is true for an offset in a method of a type whose name matches one of patterns, or in a function that
// returns such a type.
func (s *sourceFiles) belongsTo(file string, offset int, patterns []string) bool {
	syntax, lines, err := s.parse(file)
	if len(patterns) == 0 || err != nil || offset < 0 || offset > lines.Size() {
		return false
	}
	position := lines.Pos(offset)
	for _, declaration := range syntax.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || position < function.Pos() || position >= function.End() {
			continue
		}
		var types []ast.Expr
		if function.Recv != nil {
			for _, receiver := range function.Recv.List {
				types = append(types, receiver.Type)
			}
		}
		if function.Type.Results != nil {
			for _, result := range function.Type.Results.List {
				types = append(types, result.Type)
			}
		}
		return slices.ContainsFunc(types, func(t ast.Expr) bool {
			return slices.ContainsFunc(patterns, func(pattern string) bool {
				matched, _ := path.Match(pattern, typeName(t))
				return matched
			})
		})
	}
	return false
}

// typeName gives "" for a type that has no name, such as a map or a function.
func typeName(expression ast.Expr) string {
	for {
		switch e := expression.(type) {
		case *ast.StarExpr:
			expression = e.X
		case *ast.IndexExpr:
			expression = e.X
		case *ast.IndexListExpr:
			expression = e.X
		case *ast.SelectorExpr:
			return e.Sel.Name
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}
