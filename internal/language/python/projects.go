package python

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

var projectFiles = []string{"pyproject.toml", "setup.cfg", "setup.py", "pytest.ini", "tox.ini"}

type project struct {
	folder string
	path   string
}

type projects struct {
	root    string
	command []string
	mutex   sync.Mutex
	found   map[string]project
}

func newProjects(root string, command []string) *projects {
	return &projects{root: root, command: command, found: map[string]project{}}
}

func (p *projects) of(file string) project {
	start := filepath.Dir(filepath.Join(p.root, file))
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if found, seen := p.found[start]; seen {
		return found
	}
	folder := start
	for folder != p.root && !slices.ContainsFunc(projectFiles, func(name string) bool { return exists(filepath.Join(folder, name)) }) {
		parent := filepath.Dir(folder)
		if parent == folder || !strings.HasPrefix(parent, p.root) {
			folder = p.root
			break
		}
		folder = parent
	}
	path, err := filepath.Rel(p.root, folder)
	if err != nil {
		path = "."
	}
	found := project{folder: folder, path: filepath.ToSlash(path)}
	p.found[start] = found
	return found
}

func (p *projects) python(of project) []string {
	if len(p.command) == 0 {
		venv := filepath.Join(of.folder, ".venv", "bin", "python")
		if exists(venv) {
			return []string{venv}
		}
		return []string{"python3"}
	}
	command := slices.Clone(p.command)
	if strings.Contains(command[0], string(filepath.Separator)) && !filepath.IsAbs(command[0]) {
		command[0] = filepath.Join(of.folder, command[0])
	}
	return command
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
