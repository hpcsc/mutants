package operator

import "strings"

type Rule struct {
	ID           string
	Operator     string
	File         string
	OffByDefault bool
	Text         string
}

func (r Rule) IsStandard() bool {
	return strings.HasPrefix(r.File, "operators/")
}
