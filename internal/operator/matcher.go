package operator

import "context"

// Matcher takes each file as a path from the root of the repository.
type Matcher interface {
	Match(ctx context.Context, rules []Rule, files []string) ([]Match, error)
}

// Match holds the byte offsets of the text that the fix replaces, and keys Variables by name without the $.
type Match struct {
	Rule        string
	File        string
	Start, End  int
	Replacement string
	Variables   map[string]Span
}

type Span struct {
	Start, End int
}
