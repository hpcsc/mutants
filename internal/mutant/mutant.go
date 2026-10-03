package mutant

import "fmt"

// File is a path from the root of the repository. Line, Column, EndLine and EndColumn count from 1, and give
// the positions of the byte offsets Start and End.
type Mutant struct {
	ID          ID
	File        string
	Language    string
	Line        int
	Column      int
	EndLine     int
	EndColumn   int
	Start, End  int
	Operator    string
	Original    string
	Replacement string
	Bug         string
	Refs        []string
	Verdict     Verdict
}

func (m Mutant) Apply(source []byte) (string, error) {
	if m.Start < 0 || m.End > len(source) || string(source[m.Start:m.End]) != m.Original {
		return "", fmt.Errorf("%s changed after mutants read it", m.File)
	}
	return string(source[:m.Start]) + m.Replacement + string(source[m.End:]), nil
}
