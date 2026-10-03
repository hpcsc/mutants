package proposal

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

type Store struct {
	path string
}

type saved struct {
	ID string `json:"id"`
	Proposal
}

func NewStore(folder string) Store {
	return Store{path: filepath.Join(folder, "proposals.jsonl")}
}

func (s Store) Save(proposals map[string]Proposal) error {
	all, err := s.load()
	if err != nil {
		return err
	}
	maps.Copy(all, proposals)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), "proposals-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	for _, id := range slices.Sorted(maps.Keys(all)) {
		if err := encoder.Encode(saved{ID: id, Proposal: all[id]}); err != nil {
			file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}

func (s Store) Find(id string) (Proposal, bool, error) {
	all, err := s.load()
	proposal, found := all[id]
	return proposal, found, err
}

func (s Store) load() (map[string]Proposal, error) {
	all := map[string]Proposal{}
	content, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	for decoder.More() {
		var entry saved
		if err := decoder.Decode(&entry); err != nil {
			return nil, err
		}
		all[entry.ID] = entry.Proposal
	}
	return all, nil
}
