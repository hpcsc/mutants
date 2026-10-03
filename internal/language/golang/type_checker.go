package golang

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

type typeChecker struct {
	tagArguments  []string
	zeroFunctions map[string]bool
	mutex         sync.Mutex
	packages      map[string]*packages.Package
}

func newTypeChecker(tagArguments, zeroFunctions []string) *typeChecker {
	checker := &typeChecker{tagArguments: tagArguments, zeroFunctions: map[string]bool{}, packages: map[string]*packages.Package{}}
	for _, name := range zeroFunctions {
		checker.zeroFunctions[name] = true
	}
	return checker
}

// canSwap needs start at the first value, and end after the second value, of two adjacent keyed fields.
func (c *typeChecker) canSwap(path string, start, end int) bool {
	loaded, syntax, lines := c.file(path)
	if syntax == nil {
		return false
	}
	same := false
	ast.Inspect(syntax, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || same {
			return !same
		}
		for i := 0; i+1 < len(literal.Elts); i++ {
			first, firstKeyed := literal.Elts[i].(*ast.KeyValueExpr)
			second, secondKeyed := literal.Elts[i+1].(*ast.KeyValueExpr)
			if !firstKeyed || !secondKeyed || lines.Offset(first.Value.Pos()) != start || lines.Offset(second.Value.End()) != end {
				continue
			}
			if !c.isStructLiteral(literal, loaded.TypesInfo) {
				return false
			}
			firstType, secondType := loaded.TypesInfo.TypeOf(first.Value), loaded.TypesInfo.TypeOf(second.Value)
			same = firstType != nil && secondType != nil && types.Identical(firstType, secondType) && !c.isTable(literal, loaded.TypesInfo)
			return false
		}
		return true
	})
	return same
}

// canZeroField needs start at the key of a keyed field.
func (c *typeChecker) canZeroField(path string, start int) bool {
	loaded, syntax, lines := c.file(path)
	if syntax == nil {
		return false
	}
	found, canZero := false, false
	ast.Inspect(syntax, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || found {
			return !found
		}
		for _, element := range literal.Elts {
			keyed, isKeyed := element.(*ast.KeyValueExpr)
			if !isKeyed || lines.Offset(keyed.Pos()) != start {
				continue
			}
			found = true
			value, isLiteral := keyed.Value.(*ast.CompositeLit)
			isZero := c.isZero(loaded.TypesInfo.Types[keyed.Value]) || isLiteral && c.isEmpty(value, loaded.TypesInfo) ||
				c.callsZeroFunction(keyed.Value, loaded.TypesInfo)
			canZero = c.isStructLiteral(literal, loaded.TypesInfo) && !isZero
			return false
		}
		return true
	})
	return canZero
}

func (c *typeChecker) callsZeroFunction(value ast.Expr, info *types.Info) bool {
	call, isCall := ast.Unparen(value).(*ast.CallExpr)
	if !isCall {
		return false
	}
	function := ast.Unparen(call.Fun)
	switch generic := function.(type) {
	case *ast.IndexExpr:
		function = generic.X
	case *ast.IndexListExpr:
		function = generic.X
	}
	var name *ast.Ident
	switch function := function.(type) {
	case *ast.Ident:
		name = function
	case *ast.SelectorExpr:
		name = function.Sel
	default:
		return false
	}
	called, isFunction := info.Uses[name].(*types.Func)
	if !isFunction || called.Pkg() == nil || called.Signature().Recv() != nil {
		return false
	}
	return c.zeroFunctions[called.Pkg().Name()+"."+called.Name()]
}

func (c *typeChecker) isStructLiteral(literal *ast.CompositeLit, info *types.Info) bool {
	literalType := info.TypeOf(literal)
	if literalType == nil {
		return false
	}
	if pointer, ok := types.Unalias(literalType).(*types.Pointer); ok {
		literalType = pointer.Elem()
	}
	_, isStruct := literalType.Underlying().(*types.Struct)
	return isStruct
}

func (c *typeChecker) isTable(literal *ast.CompositeLit, info *types.Info) bool {
	var named types.Type
	for _, element := range literal.Elts {
		keyed, isKeyed := element.(*ast.KeyValueExpr)
		if !isKeyed {
			return false
		}
		value, isLiteral := ast.Unparen(keyed.Value).(*ast.CompositeLit)
		if !isLiteral {
			return false
		}
		valueType := info.TypeOf(value)
		if _, isNamed := types.Unalias(valueType).(*types.Named); !isNamed || named != nil && !types.Identical(named, valueType) {
			return false
		}
		named = valueType
		for _, field := range value.Elts {
			if keyedField, ok := field.(*ast.KeyValueExpr); ok {
				field = keyedField.Value
			}
			if info.Types[field].Value == nil {
				return false
			}
		}
	}
	return named != nil
}

func (c *typeChecker) zeroOfSlot(path string, start, end int) string {
	slot, value, info := c.returnSlot(path, start, end)
	return c.zeroOf(slot, value, info)
}

func (c *typeChecker) zeroOfParameter(path string, start, end int) string {
	slot, value, info := c.parameterSlot(path, start, end)
	return c.zeroOf(slot, value, info)
}

func (c *typeChecker) zeroOf(slot types.Type, value ast.Expr, info *types.Info) string {
	if slot == nil || c.isError(slot) || c.isZero(info.Types[value]) {
		return ""
	}
	if _, isParameter := types.Unalias(slot).(*types.TypeParam); isParameter {
		return ""
	}
	switch underlying := slot.Underlying().(type) {
	case *types.Basic:
		switch {
		case underlying.Info()&types.IsBoolean != 0:
			return "false"
		case underlying.Info()&types.IsString != 0:
			return `""`
		case underlying.Info()&types.IsNumeric != 0:
			return "0"
		case underlying.Kind() == types.UnsafePointer:
			return "nil"
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return "nil"
	case *types.Struct:
		literal, isLiteral := value.(*ast.CompositeLit)
		if isLiteral && literal.Type != nil && !c.isEmpty(literal, info) {
			return types.ExprString(literal.Type) + "{}"
		}
	}
	return ""
}

func (c *typeChecker) isEmpty(literal *ast.CompositeLit, info *types.Info) bool {
	for _, element := range literal.Elts {
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			element = keyed.Value
		}
		if !c.isZero(info.Types[element]) && !c.callsZeroFunction(element, info) && !c.isEmptyStruct(element, info) {
			return false
		}
	}
	return true
}

// isEmptyStruct takes no elided &T{}, such as the item of []*T{{}}, because a pointer to an empty struct is not
// nil.
func (c *typeChecker) isEmptyStruct(value ast.Expr, info *types.Info) bool {
	literal, isLiteral := ast.Unparen(value).(*ast.CompositeLit)
	if !isLiteral || info.TypeOf(literal) == nil {
		return false
	}
	_, isStruct := info.TypeOf(literal).Underlying().(*types.Struct)
	return isStruct && c.isEmpty(literal, info)
}

func (c *typeChecker) isErrorSlot(path string, start, end int) bool {
	slot, _, _ := c.returnSlot(path, start, end)
	return slot != nil && c.isError(slot)
}

func (c *typeChecker) canBecomeTrue(path string, start, end int) bool {
	slot, value, info := c.returnSlot(path, start, end)
	if slot == nil {
		return false
	}
	basic, isBasic := slot.Underlying().(*types.Basic)
	constantValue := info.Types[value].Value
	isTrue := constantValue != nil && constantValue.Kind() == constant.Bool && constant.BoolVal(constantValue)
	return isBasic && basic.Info()&types.IsBoolean != 0 && !isTrue
}

func (c *typeChecker) callsTimeMethod(path string, start, end int) bool {
	loaded, syntax, lines := c.file(path)
	if syntax == nil || start < 0 || end > lines.Size() {
		return false
	}
	enclosing, _ := astutil.PathEnclosingInterval(syntax, lines.Pos(start), lines.Pos(end))
	if len(enclosing) == 0 {
		return false
	}
	expression, _ := enclosing[0].(ast.Expr)
	if negation, ok := expression.(*ast.UnaryExpr); ok && negation.Op == token.NOT {
		expression = negation.X
	}
	call, isCall := ast.Unparen(expression).(*ast.CallExpr)
	if !isCall {
		return false
	}
	selector, isSelector := call.Fun.(*ast.SelectorExpr)
	if !isSelector {
		return false
	}
	method, isMethod := loaded.TypesInfo.Uses[selector.Sel].(*types.Func)
	return isMethod && strings.HasPrefix(method.FullName(), "(time.Time).")
}

func (c *typeChecker) isError(slot types.Type) bool {
	return types.Identical(slot, types.Universe.Lookup("error").Type())
}

func (c *typeChecker) isZero(value types.TypeAndValue) bool {
	switch {
	case value.IsNil():
		return true
	case value.Value == nil:
		return false
	case value.Value.Kind() == constant.Bool:
		return !constant.BoolVal(value.Value)
	case value.Value.Kind() == constant.String:
		return constant.StringVal(value.Value) == ""
	case value.Value.Kind() == constant.Unknown:
		return false
	}
	return constant.Sign(value.Value) == 0
}

func (c *typeChecker) returnSlot(path string, start, end int) (types.Type, ast.Expr, *types.Info) {
	loaded, syntax, lines := c.file(path)
	if syntax == nil || start < 0 || end > lines.Size() {
		return nil, nil, nil
	}
	enclosing, _ := astutil.PathEnclosingInterval(syntax, lines.Pos(start), lines.Pos(end))
	var statement *ast.ReturnStmt
	for _, node := range enclosing {
		if found, ok := node.(*ast.ReturnStmt); ok {
			statement = found
			break
		}
	}
	if statement == nil {
		return nil, nil, nil
	}
	index := -1
	for i, value := range statement.Results {
		if lines.Offset(value.Pos()) == start && lines.Offset(value.End()) == end {
			index = i
		}
	}
	if index < 0 {
		return nil, nil, nil
	}
	var signature *types.Signature
	for _, node := range enclosing {
		switch function := node.(type) {
		case *ast.FuncLit:
			signature, _ = loaded.TypesInfo.TypeOf(function).(*types.Signature)
		case *ast.FuncDecl:
			if object := loaded.TypesInfo.Defs[function.Name]; object != nil {
				signature, _ = object.Type().(*types.Signature)
			}
		default:
			continue
		}
		break
	}
	if signature == nil || signature.Results().Len() != len(statement.Results) {
		return nil, nil, nil
	}
	return signature.Results().At(index).Type(), statement.Results[index], loaded.TypesInfo
}

func (c *typeChecker) parameterSlot(path string, start, end int) (types.Type, ast.Expr, *types.Info) {
	loaded, syntax, lines := c.file(path)
	if syntax == nil || start < 0 || end > lines.Size() {
		return nil, nil, nil
	}
	enclosing, _ := astutil.PathEnclosingInterval(syntax, lines.Pos(start), lines.Pos(end))
	if len(enclosing) < 2 {
		return nil, nil, nil
	}
	call, isCall := enclosing[1].(*ast.CallExpr)
	if !isCall {
		return nil, nil, nil
	}
	index := slices.IndexFunc(call.Args, func(argument ast.Expr) bool { return argument == enclosing[0] })
	function := loaded.TypesInfo.Types[call.Fun]
	if index < 0 || function.Type == nil || function.IsType() || function.IsBuiltin() {
		return nil, nil, nil
	}
	signature, isSignature := function.Type.Underlying().(*types.Signature)
	if !isSignature {
		return nil, nil, nil
	}
	fixed := signature.Params().Len()
	if signature.Variadic() {
		fixed--
	}
	if index >= fixed || len(call.Args) < fixed {
		return nil, nil, nil
	}
	slot := signature.Params().At(index).Type()
	if c.isContext(slot) || c.isErrorText(signature, slot, loaded.TypesInfo.Types[call.Args[index]]) {
		return nil, nil, nil
	}
	return slot, call.Args[index], loaded.TypesInfo
}

func (c *typeChecker) isContext(slot types.Type) bool {
	named, isNamed := types.Unalias(slot).(*types.Named)
	return isNamed && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "context" && named.Obj().Name() == "Context"
}

func (c *typeChecker) isErrorText(signature *types.Signature, slot types.Type, value types.TypeAndValue) bool {
	results := signature.Results()
	basic, isBasic := slot.Underlying().(*types.Basic)
	isText := isBasic && basic.Info()&types.IsString != 0 && value.Value != nil
	return isText && results.Len() == 1 && c.isError(results.At(0).Type())
}

func (c *typeChecker) file(path string) (*packages.Package, *ast.File, *token.File) {
	loaded := c.load(filepath.Dir(path))
	if loaded == nil {
		return nil, nil, nil
	}
	for _, syntax := range loaded.Syntax {
		lines := loaded.Fset.File(syntax.Pos())
		if lines != nil && lines.Name() == path {
			return loaded, syntax, lines
		}
	}
	return nil, nil, nil
}

func (c *typeChecker) load(folder string) *packages.Package {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if loaded, found := c.packages[folder]; found {
		return loaded
	}
	config := &packages.Config{
		Mode:       packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:        folder,
		BuildFlags: c.tagArguments,
	}
	var loaded *packages.Package
	if found, err := packages.Load(config, "."); err == nil && len(found) == 1 && len(found[0].Errors) == 0 {
		loaded = found[0]
	}
	c.packages[folder] = loaded
	return loaded
}
