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
	Base       string   `yaml:"base"`
	Workers    int      `yaml:"workers"`
	Operators  []string `yaml:"operators"`
	Exclude    []string `yaml:"exclude"`
	CallerGaps bool     `yaml:"caller_gaps"`
	Go         goConfig `yaml:"go"`
}

type goConfig struct {
	Tags []string `yaml:"tags"`
	// ZeroFunctions names each function as package.Function, with the name of the package and not its path.
	ZeroFunctions []string `yaml:"zero_functions"`
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
	if err := checkKeys(document.Content[0], reflect.TypeFor[config](), ""); err != nil {
		return config{}, err
	}
	if err := document.Decode(&loaded); err != nil {
		return config{}, fmt.Errorf("read .mutants.yml: %w", err)
	}
	return loaded, nil
}

// checkKeys gives section as the key of the mapping, and "" for the root of the file.
func checkKeys(mapping *yaml.Node, settings reflect.Type, section string) error {
	keys := keysOf(settings)
	if mapping.Kind != yaml.MappingNode {
		if section != "" {
			return fmt.Errorf("%s in .mutants.yml (line %d) must hold keys and values, and the keys are %s", section, mapping.Line, strings.Join(keys, ", "))
		}
		return fmt.Errorf(".mutants.yml must hold keys and values, and the keys are %s", strings.Join(keys, ", "))
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		index := slices.Index(keys, key.Value)
		if index < 0 && section != "" {
			return fmt.Errorf("unknown key %s.%s in .mutants.yml (line %d): the keys of %s are %s", section, key.Value, key.Line, section, strings.Join(keys, ", "))
		}
		if index < 0 {
			return fmt.Errorf("unknown key %s in .mutants.yml (line %d): the keys are %s", key.Value, key.Line, strings.Join(keys, ", "))
		}
		if field := settings.Field(index).Type; field.Kind() == reflect.Struct && value.Tag != "!!null" {
			if err := checkKeys(value, field, key.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func keysOf(settings reflect.Type) []string {
	keys := make([]string, 0, settings.NumField())
	for i := range settings.NumField() {
		keys = append(keys, settings.Field(i).Tag.Get("yaml"))
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
	return c.Go.Tags
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
