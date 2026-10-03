package proposal

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const Operator = "PROPOSED"

type Proposal struct {
	File string `json:"file"`
	Old  string `json:"old"`
	New  string `json:"new"`
	Bug  string `json:"bug"`
	Ref  string `json:"ref,omitempty"`
}

type Rejection struct {
	Proposal Proposal
	Reason   string
}

type Summary struct {
	Accepted int
	Rejected []Rejection
}

func (p Proposal) Number() int {
	sum := sha256.Sum256([]byte(p.Old + "\x00" + p.New))
	return int(binary.BigEndian.Uint32(sum[:4])%900000) + 100000
}

func (p Proposal) RepositoryFile() (file, reason string) {
	file = filepath.ToSlash(filepath.Clean(p.File))
	if filepath.IsAbs(file) || file == ".." || strings.HasPrefix(file, "../") {
		return "", "the file is not in the repository"
	}
	return file, ""
}

func (p Proposal) Start(source []byte) (start int, reason string) {
	switch count := strings.Count(string(source), p.Old); {
	case count == 0:
		return 0, "old not found"
	case count > 1:
		return 0, fmt.Sprintf("old found %d times", count)
	case p.Old == p.New:
		return 0, "old and new are the same"
	}
	return strings.Index(string(source), p.Old), ""
}

func Read(r io.Reader) ([]Proposal, error) {
	lines := bufio.NewReader(r)
	var proposals []Proposal
	for number := 1; ; number++ {
		line, err := lines.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		if strings.TrimSpace(line) != "" {
			proposal, parseErr := parse(line)
			if parseErr != nil {
				return nil, fmt.Errorf("line %d: %w", number, parseErr)
			}
			proposals = append(proposals, proposal)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if len(proposals) == 0 {
		return nil, errors.New("the file holds no proposal")
	}
	return proposals, nil
}

func parse(line string) (Proposal, error) {
	var fields struct {
		File *string `json:"file"`
		Old  *string `json:"old"`
		New  *string `json:"new"`
		Bug  *string `json:"bug"`
		Ref  string  `json:"ref"`
	}
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return Proposal{}, fmt.Errorf("a proposal is one JSON object: %w", err)
	}
	for _, field := range []struct {
		name  string
		value *string
	}{{"file", fields.File}, {"old", fields.Old}, {"new", fields.New}, {"bug", fields.Bug}} {
		if field.value == nil {
			return Proposal{}, fmt.Errorf(`the proposal has no "%s"`, field.name)
		}
	}
	if *fields.Old == "" {
		return Proposal{}, errors.New(`"old" is empty`)
	}
	return Proposal{File: *fields.File, Old: *fields.Old, New: *fields.New, Bug: *fields.Bug, Ref: fields.Ref}, nil
}
