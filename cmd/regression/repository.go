package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"go.yaml.in/yaml/v3"
)

var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type repository struct {
	Name     string   `yaml:"name"`
	URL      string   `yaml:"url"`
	Commit   string   `yaml:"commit"`
	Language string   `yaml:"language"`
	Install  []string `yaml:"install"`
}

func readRepositories(path string) ([]repository, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var repositories []repository
	if err := yaml.Unmarshal(content, &repositories); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, r := range repositories {
		if err := r.check(); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
	return repositories, nil
}

func (r repository) check() error {
	switch {
	case r.Name == "" || r.URL == "":
		return errors.New("each repository needs a name and a url")
	case !fullCommit.MatchString(r.Commit):
		return fmt.Errorf("%s: the commit must be a full sha, not %q", r.Name, r.Commit)
	case r.Language == "go" && len(r.Install) > 0:
		return fmt.Errorf("%s: install is for a python repository only", r.Name)
	case r.Language == "python" && len(r.Install) == 0:
		return fmt.Errorf("%s: a python repository needs install, the arguments of pip install", r.Name)
	case r.Language != "go" && r.Language != "python":
		return fmt.Errorf("%s: the language is go or python, not %q", r.Name, r.Language)
	}
	return nil
}

func (r repository) sourcePathspecs() []string {
	if r.Language == "go" {
		return []string{":(glob)**/*.go", ":(glob,exclude)**/*_test.go", ":(glob,exclude)**/testdata/**"}
	}
	return []string{":(glob)**/*.py", ":(glob,exclude)**/tests/**", ":(glob,exclude)**/test_*.py", ":(glob,exclude)**/conftest.py", ":(glob,exclude)docs/**"}
}
