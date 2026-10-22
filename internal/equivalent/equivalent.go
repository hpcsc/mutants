package equivalent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hpcsc/mutants/internal/mutant"
	"go.yaml.in/yaml/v3"
)

const fileName = "equivalent.yml"

var keys = []string{"id", "original", "replacement", "reason"}

type Entry struct {
	ID          mutant.ID
	Original    string
	Replacement string
	Reason      string
	Line        int
}

func (e Entry) Matches(m mutant.Mutant) bool {
	return e.ID.File == m.ID.File && e.ID.Function == m.ID.Function && e.ID.Operator == m.ID.Operator &&
		oneLine(e.Original) == oneLine(m.Original) && oneLine(e.Replacement) == oneLine(m.Replacement)
}

type List struct {
	File    string
	Entries []Entry
}

// Verdict makes a LIVED mutant that an entry fits EQUIVALENT, with the reason of the entry as the detail.
func (l List) Verdict(m mutant.Mutant) mutant.Verdict {
	index := slices.IndexFunc(l.Entries, func(e Entry) bool { return e.Matches(m) })
	if m.Verdict.Status != mutant.Lived || index < 0 {
		return m.Verdict
	}
	return mutant.Verdict{Status: mutant.Equivalent, Detail: l.Entries[index].Reason}
}

func Load(root, sharedGitFolder string, stderr io.Writer) (List, error) {
	own, shared := filepath.Join(root, ".mutants", fileName), filepath.Join(sharedGitFolder, "mutants", fileName)
	path := own
	content, err := os.ReadFile(own)
	if err == nil && sharedGitFolder != "" {
		if _, sharedErr := os.Stat(shared); sharedErr == nil {
			fmt.Fprintf(stderr, "mutants ignores %s, because %s exists\n", messagePath(root, shared), messagePath(root, own))
		}
	}
	if errors.Is(err, os.ErrNotExist) && sharedGitFolder != "" {
		path = shared
		content, err = os.ReadFile(shared)
	}
	if errors.Is(err, os.ErrNotExist) {
		return List{}, nil
	}
	if err != nil {
		return List{}, err
	}
	return Read(messagePath(root, path), content)
}

func Read(file string, content []byte) (List, error) {
	list := List{File: file}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return List{}, fmt.Errorf("read %s: %w", file, err)
	}
	if len(document.Content) == 0 {
		return list, nil
	}
	entries := document.Content[0]
	if entries.Kind != yaml.SequenceNode {
		return List{}, fmt.Errorf("%s must hold a list of equivalent mutants, each with the keys %s", file, strings.Join(keys, ", "))
	}
	for _, node := range entries.Content {
		entry, err := readEntry(file, node)
		if err != nil {
			return List{}, err
		}
		list.Entries = append(list.Entries, entry)
	}
	return list, nil
}

func readEntry(file string, node *yaml.Node) (Entry, error) {
	if node.Kind != yaml.MappingNode {
		return Entry{}, fmt.Errorf("the entry in %s (line %d) must hold the keys %s", file, node.Line, strings.Join(keys, ", "))
	}
	values := map[string]string{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if !slices.Contains(keys, key.Value) {
			return Entry{}, fmt.Errorf("unknown key %s in %s (line %d): the keys are %s", key.Value, file, key.Line, strings.Join(keys, ", "))
		}
		if value.Kind != yaml.ScalarNode {
			return Entry{}, fmt.Errorf("%s in %s (line %d) must be a text", key.Value, file, value.Line)
		}
		if value.Tag != "!!null" {
			values[key.Value] = value.Value
		} else {
			values[key.Value] = ""
		}
	}
	for _, key := range keys {
		if _, found := values[key]; !found {
			return Entry{}, fmt.Errorf("the entry in %s (line %d) has no %s", file, node.Line, key)
		}
	}
	id, err := mutant.ParseID(values["id"])
	if err != nil {
		return Entry{}, fmt.Errorf("the entry in %s (line %d): %w", file, node.Line, err)
	}
	if values["original"] == "" {
		return Entry{}, fmt.Errorf("the entry in %s (line %d) has an empty original", file, node.Line)
	}
	if strings.TrimSpace(values["reason"]) == "" {
		return Entry{}, fmt.Errorf("the entry in %s (line %d) has no reason: the reason tells a reviewer why no test can kill the mutant", file, node.Line)
	}
	return Entry{ID: id, Original: values["original"], Replacement: values["replacement"], Reason: values["reason"], Line: node.Line}, nil
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func messagePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return path
	}
	return relative
}
