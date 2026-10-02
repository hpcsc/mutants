package proposal

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Operator takes the place of the operator in the id of a proposed mutant.
const Operator = "PROPOSED"

// File is a path from the root of the repository.
type Proposal struct {
	File string `json:"file"`
	Old  string `json:"old"`
	New  string `json:"new"`
	Bug  string `json:"bug"`
}

type Rejection struct {
	Proposal Proposal
	Reason   string
}

// Number gives the same edit the same number, whatever the other proposals are.
func (p Proposal) Number() int {
	sum := sha256.Sum256([]byte(p.Old + "\x00" + p.New))
	return int(binary.BigEndian.Uint32(sum[:4])%900000) + 100000
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
	return Proposal{File: *fields.File, Old: *fields.Old, New: *fields.New, Bug: *fields.Bug}, nil
}
