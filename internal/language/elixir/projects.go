package elixir

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type project struct {
	folder string
	path   string
}

type projects struct {
	root  string
	mutex sync.Mutex
	found map[string]project
}

func newProjects(root string) *projects {
	return &projects{root: root, found: map[string]project{}}
}

// of gives the nearest folder above the file with a mix.exs, and false when no folder in the repository has one.
func (p *projects) of(file string) (project, bool) {
	start := filepath.Dir(filepath.Join(p.root, file))
	p.mutex.Lock()
	defer p.mutex.Unlock()
	found, seen := p.found[start]
	if !seen {
		found = p.find(start)
		p.found[start] = found
	}
	return found, found.folder != ""
}

func (p *projects) find(start string) project {
	for folder := start; strings.HasPrefix(folder, p.root); folder = filepath.Dir(folder) {
		if _, err := os.Stat(filepath.Join(folder, "mix.exs")); err == nil {
			path, err := filepath.Rel(p.root, folder)
			if err != nil {
				return project{}
			}
			return project{folder: folder, path: filepath.ToSlash(path)}
		}
		if folder == p.root {
			break
		}
	}
	return project{}
}
