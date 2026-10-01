package operator

// Edit holds Start and End as byte offsets in File.
type Edit struct {
	File        string
	Operator    string
	Rule        string
	Start, End  int
	Original    string
	Replacement string
}
