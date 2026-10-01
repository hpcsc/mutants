package astgrep

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hpcsc/mutants/internal/operator"
)

// filesPerScan keeps the arguments of one scan below the limit of the operating system.
const filesPerScan = 1000

type scanner struct {
	root string
}

func New(root string) operator.Matcher {
	return &scanner{root: root}
}

func (s *scanner) Match(ctx context.Context, rules []operator.Rule, files []string) ([]operator.Match, error) {
	if len(rules) == 0 || len(files) == 0 {
		return nil, nil
	}
	folder, err := os.MkdirTemp("", "mutants-rules-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(folder)
	documents := make([]string, 0, len(rules))
	for _, rule := range rules {
		documents = append(documents, strings.TrimSpace(rule.Text))
	}
	ruleFile := filepath.Join(folder, "rules.yml")
	if err := os.WriteFile(ruleFile, []byte(strings.Join(documents, "\n---\n")+"\n"), 0o600); err != nil {
		return nil, err
	}

	var matches []operator.Match
	for start := 0; start < len(files); start += filesPerScan {
		found, err := s.scan(ctx, ruleFile, files[start:min(start+filesPerScan, len(files))])
		if err != nil {
			return nil, err
		}
		matches = append(matches, found...)
	}
	return matches, nil
}

func (s *scanner) scan(ctx context.Context, ruleFile string, files []string) ([]operator.Match, error) {
	// --hint runs every rule at the level hint, so a rule with severity error does not give exit code 1
	command := exec.CommandContext(ctx, "ast-grep", append([]string{"scan", "--rule", ruleFile, "--json=stream", "--hint", "--"}, files...)...)
	command.Dir = s.root
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("ast-grep scan: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	type span struct {
		Start int `json:"start"`
		End   int `json:"end"`
	}
	var matches []operator.Match
	lines := bufio.NewScanner(bytes.NewReader(output))
	lines.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for lines.Scan() {
		var found struct {
			RuleID      string  `json:"ruleId"`
			File        string  `json:"file"`
			Replacement *string `json:"replacement"`
			Range       struct {
				ByteOffset span `json:"byteOffset"`
			} `json:"range"`
			ReplacementOffsets *span `json:"replacementOffsets"`
			MetaVariables      struct {
				Single map[string]struct {
					Range struct {
						ByteOffset span `json:"byteOffset"`
					} `json:"range"`
				} `json:"single"`
			} `json:"metaVariables"`
		}
		if err := json.Unmarshal(lines.Bytes(), &found); err != nil {
			return nil, fmt.Errorf("read the output of ast-grep scan: %w", err)
		}
		match := operator.Match{
			Rule:      found.RuleID,
			File:      filepath.ToSlash(found.File),
			Start:     found.Range.ByteOffset.Start,
			End:       found.Range.ByteOffset.End,
			Variables: map[string]operator.Span{},
		}
		if found.Replacement != nil {
			match.Replacement = *found.Replacement
		}
		if found.ReplacementOffsets != nil {
			match.Start, match.End = found.ReplacementOffsets.Start, found.ReplacementOffsets.End
		}
		for name, variable := range found.MetaVariables.Single {
			match.Variables[name] = operator.Span{Start: variable.Range.ByteOffset.Start, End: variable.Range.ByteOffset.End}
		}
		matches = append(matches, match)
	}
	return matches, lines.Err()
}

var _ operator.Matcher = (*scanner)(nil)
