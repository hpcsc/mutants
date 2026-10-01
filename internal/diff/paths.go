package diff

import "path"

type Paths struct {
	Extensions []string
	Exclude    []string
}

func (p Paths) pathspecs(folders ...string) []string {
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
