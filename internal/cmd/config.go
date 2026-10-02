package cmd

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"
)

const (
	defaultBase    = "origin/HEAD"
	defaultWorkers = 4
)

type config struct {
	Base      string   `yaml:"base"`
	Workers   int      `yaml:"workers"`
	Tags      []string `yaml:"tags"`
	Operators []string `yaml:"operators"`
	Exclude   []string `yaml:"exclude"`
	// ZeroFunctions names each function as package.Function, with the name of the package and not its path.
	ZeroFunctions []string `yaml:"zero_functions"`
	CallerGaps    bool     `yaml:"caller_gaps"`
}

func loadConfig(root string) (config, error) {
	content, err := os.ReadFile(filepath.Join(root, ".mutants.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return config{}, nil
	}
	if err != nil {
		return config{}, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return config{}, fmt.Errorf("read .mutants.yml: %w", err)
	}
	if len(document.Content) == 0 {
		return config{}, nil
	}
	var loaded config
	if err := loaded.checkKeys(document.Content[0]); err != nil {
		return config{}, err
	}
	if err := document.Decode(&loaded); err != nil {
		return config{}, fmt.Errorf("read .mutants.yml: %w", err)
	}
	return loaded, nil
}

func (c config) checkKeys(mapping *yaml.Node) error {
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf(".mutants.yml must hold keys and values, and the keys are %s", strings.Join(c.keys(), ", "))
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if !slices.Contains(c.keys(), key.Value) {
			return fmt.Errorf("unknown key %s in .mutants.yml (line %d): the keys are %s", key.Value, key.Line, strings.Join(c.keys(), ", "))
		}
	}
	return nil
}

func (c config) keys() []string {
	fields := reflect.TypeFor[config]()
	keys := make([]string, 0, fields.NumField())
	for i := range fields.NumField() {
		keys = append(keys, fields.Field(i).Tag.Get("yaml"))
	}
	return keys
}

func (c config) base(cmd *cli.Command) string {
	if cmd.IsSet("base") {
		return cmd.String("base")
	}
	return cmp.Or(c.Base, defaultBase)
}

func (c config) workers(cmd *cli.Command) int {
	if cmd.IsSet("workers") {
		return cmd.Int("workers")
	}
	return cmp.Or(c.Workers, defaultWorkers)
}

func (c config) tags(cmd *cli.Command) []string {
	if cmd.IsSet("tags") {
		return cmd.StringSlice("tags")
	}
	return c.Tags
}

func (c config) callerGaps(cmd *cli.Command) bool {
	if cmd.IsSet("caller-gaps") {
		return cmd.Bool("caller-gaps")
	}
	return c.CallerGaps
}

func (c config) operators(cmd *cli.Command) []string {
	if cmd.IsSet("operators") {
		return cmd.StringSlice("operators")
	}
	return c.Operators
}
