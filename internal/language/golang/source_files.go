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

func (s *sourceFiles) causesMissingReturn(file string, start, end int) bool {
	syntax, lines, err := s.parse(file)
	if err != nil || start < 0 || end > lines.Size() {
		return false
	}
	enclosing, _ := astutil.PathEnclosingInterval(syntax, lines.Pos(start), lines.Pos(end))
	for _, node := range enclosing {
		var signature *ast.FuncType
		var body *ast.BlockStmt
		switch function := node.(type) {
		case *ast.FuncDecl:
			signature, body = function.Type, function.Body
		case *ast.FuncLit:
			signature, body = function.Type, function.Body
		default:
			continue
		}
		if body == nil || signature.Results == nil || len(signature.Results.List) == 0 {
			return false
		}
		emptied := func(statement ast.Stmt) bool {
			return lines.Offset(statement.Pos()) >= start && lines.Offset(statement.End()) <= end
		}
		kept := func(ast.Stmt) bool { return false }
		return terminates(body.List, kept) && !terminates(body.List, emptied)
	}
	return false
}

func terminates(statements []ast.Stmt, emptied func(ast.Stmt) bool) bool {
	for i := len(statements) - 1; i >= 0; i-- {
		if _, empty := statements[i].(*ast.EmptyStmt); empty || emptied(statements[i]) {
			continue
		}
		return terminating(statements[i], "", emptied)
	}
	return false
}

// terminating must agree with the compiler, which follows "Terminating statements" in the Go specification
func terminating(statement ast.Stmt, label string, emptied func(ast.Stmt) bool) bool {
	switch statement := statement.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return statement.Tok == token.GOTO
	case *ast.ExprStmt:
		call, isCall := statement.X.(*ast.CallExpr)
		function, isIdentifier := call.Fun.(*ast.Ident)
		return isCall && isIdentifier && function.Name == "panic"
	case *ast.BlockStmt:
		return terminates(statement.List, emptied)
	case *ast.IfStmt:
		return statement.Else != nil && terminates(statement.Body.List, emptied) && terminating(statement.Else, "", emptied)
	case *ast.LabeledStmt:
		return terminating(statement.Stmt, statement.Label.Name, emptied)
	case *ast.ForStmt:
		return statement.Cond == nil && !breaksOut(statement.Body, label, emptied)
	case *ast.SwitchStmt:
		return casesTerminate(statement.Body, label, emptied)
	case *ast.TypeSwitchStmt:
		return casesTerminate(statement.Body, label, emptied)
	case *ast.SelectStmt:
		return casesTerminate(statement.Body, label, emptied)
	}
	return false
}

func casesTerminate(body *ast.BlockStmt, label string, emptied func(ast.Stmt) bool) bool {
	if breaksOut(body, label, emptied) {
		return false
	}
	hasDefault := false
	for _, clause := range body.List {
		var statements []ast.Stmt
		switch clause := clause.(type) {
		case *ast.CaseClause:
			hasDefault = hasDefault || clause.List == nil
			statements = clause.Body
		case *ast.CommClause:
			hasDefault = true
			statements = clause.Body
		}
		if !terminates(statements, emptied) && !endsInFallthrough(statements, emptied) {
			return false
		}
	}
	return hasDefault
}

func endsInFallthrough(statements []ast.Stmt, emptied func(ast.Stmt) bool) bool {
	if len(statements) == 0 || emptied(statements[len(statements)-1]) {
		return false
	}
	branch, isBranch := statements[len(statements)-1].(*ast.BranchStmt)
	return isBranch && branch.Tok == token.FALLTHROUGH
}

// a break with no label leaves only the innermost for, switch or select around it
func breaksOut(body ast.Node, label string, emptied func(ast.Stmt) bool) bool {
	found := false
	var visit func(node ast.Node, nested bool)
	visit = func(node ast.Node, nested bool) {
		ast.Inspect(node, func(child ast.Node) bool {
			if found || child == nil {
				return false
			}
			if statement, isStatement := child.(ast.Stmt); isStatement && emptied(statement) {
				return false
			}
			switch child := child.(type) {
			case *ast.FuncLit:
				return false
			case *ast.BranchStmt:
				found = child.Tok == token.BREAK && ((child.Label == nil && !nested) || (child.Label != nil && child.Label.Name == label))
				return false
			case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
				if child != node {
					visit(child, true)
					return false
				}
			}
			return true
		})
	}
	visit(body, false)
	return found
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
