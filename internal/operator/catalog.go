package operator

type defaultRun int

const (
	onByDefault defaultRun = iota
	offByDefault
)

// add an operator only when each language can make its change
var catalog = map[string]defaultRun{
	"ARGUMENT_EMPTY":        offByDefault,
	"ARITHMETIC_BASE":       onByDefault,
	"BRANCH_CASE":           onByDefault,
	"BRANCH_ELSE":           onByDefault,
	"BRANCH_IF":             onByDefault,
	"BREAK_AT_END":          onByDefault,
	"BREAK_AT_START":        onByDefault,
	"CONDITIONALS_BOUNDARY": onByDefault,
	"CONDITIONALS_NEGATION": onByDefault,
	"ERROR_CAUSE_REMOVE":    offByDefault,
	"ERROR_REMOVE":          onByDefault,
	"EXPRESSION_REMOVE":     onByDefault,
	"INCREMENT_DECREMENT":   onByDefault,
	"INTEGER_DECREMENT":     onByDefault,
	"INTEGER_INCREMENT":     onByDefault,
	"INVERT_LOGICAL":        onByDefault,
	"NAMED_VALUE_REMOVE":    onByDefault,
	"NAMED_VALUE_SWAP":      onByDefault,
	"REMOVE_LOGICAL_NOT":    onByDefault,
	"RETURN_EMPTY":          onByDefault,
	"RETURN_TRUE":           onByDefault,
	"STATEMENT_REMOVE":      onByDefault,
}
