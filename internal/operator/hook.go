package operator

type Hook interface {
	Operator() string
	Edits(source []byte, matches []Match) []Edit
}
