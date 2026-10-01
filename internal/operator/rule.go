package operator

type Rule struct {
	ID           string
	Operator     string
	File         string
	OffByDefault bool
	Text         string
}
