package diff

import (
	"maps"
	"path"
	"slices"
)

type Lines struct {
	files map[string]map[int]bool
}

func (l *Lines) Add(file string, first, last int) {
	if first > last {
		return
	}
	if l.files == nil {
		l.files = map[string]map[int]bool{}
	}
	lines := l.files[file]
	if lines == nil {
		lines = map[int]bool{}
		l.files[file] = lines
	}
	for line := first; line <= last; line++ {
		lines[line] = true
	}
}

func (l Lines) WithExtensions(extensions []string) Lines {
	var kept Lines
	for file, lines := range l.files {
		if slices.Contains(extensions, path.Ext(file)) {
			if kept.files == nil {
				kept.files = map[string]map[int]bool{}
			}
			kept.files[file] = lines
		}
	}
	return kept
}

func (l Lines) Has(file string, line int) bool {
	return l.files[file][line]
}

func (l Lines) Touches(file string, first, last int) bool {
	for line := first; line <= last; line++ {
		if l.files[file][line] {
			return true
		}
	}
	return false
}

func (l Lines) Files() []string {
	return slices.Sorted(maps.Keys(l.files))
}

func (l Lines) Len() int {
	count := 0
	for _, lines := range l.files {
		count += len(lines)
	}
	return count
}
