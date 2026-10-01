package mutant

// File is a path from the root of the repository. Line, Column, EndLine and EndColumn count from 1, and give
// the positions of the byte offsets Start and End.
type Mutant struct {
	ID          ID
	File        string
	Line        int
	Column      int
	EndLine     int
	EndColumn   int
	Start, End  int
	Operator    string
	Original    string
	Replacement string
	Result      Result
}
