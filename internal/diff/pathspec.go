package diff

import "path"

// IgnoreSpaceChange leaves out a line whose only change is the amount of white space in it, so it must be
// false for a language in which indentation matters.
type Pathspec struct {
	Extensions        []string
	Exclude           []string
	IgnoreSpaceChange bool
}

func (p Pathspec) patterns(folders ...string) []string {
	if len(folders) == 0 {
		folders = []string{"**"}
	}
	var specs []string
	for _, folder := range folders {
		for _, extension := range p.Extensions {
			specs = append(specs, ":(glob)"+path.Join(folder, "*"+extension))
		}
	}
	for _, glob := range p.Exclude {
		specs = append(specs, ":(glob,exclude)"+glob)
	}
	return specs
}
