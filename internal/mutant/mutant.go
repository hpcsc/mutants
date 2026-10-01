package mutant

// Mutant is one small change to the code. Line and Column give the start of the change, from 1, and Start
// and End are its byte offsets in File, a path from the root of the repository.
type Mutant struct {
	ID          ID
	File        string
	Line        int
	Column      int
	Start, End  int
	Operator    string
	Original    string
	Replacement string
	Result      Result
}
