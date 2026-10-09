package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/hpcsc/mutants/internal/language/python"
	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"
)

const (
	defaultBase    = "origin/HEAD"
	defaultWorkers = 4
)

type config struct {
	Base       string       `yaml:"base"`
	Workers    int          `yaml:"workers"`
	Operators  []string     `yaml:"operators"`
	Exclude    []string     `yaml:"exclude"`
	CallerGaps bool         `yaml:"caller_gaps"`
	Go         goConfig     `yaml:"go"`
	Python     pythonConfig `yaml:"python"`
}

type goConfig struct {
	Tags          []string `yaml:"tags"`
	ZeroFunctions []string `yaml:"zero_functions"`
	ExcludeTypes  []string `yaml:"exclude_types"`
}

type pythonConfig struct {
	Command []string `yaml:"command"`
}

func repositoryConfig(ctx context.Context, repository *diff.Repository, stderr io.Writer) (config, error) {
	sharedGitFolder, err := repository.SharedGitFolder(ctx)
	if err != nil {
		return config{}, err
	}
	return loadConfig(repository.Root(), sharedGitFolder, stderr)
}

func loadConfig(root, sharedGitFolder string, stderr io.Writer) (config, error) {
	path, local := filepath.Join(root, ".mutants.yml"), filepath.Join(sharedGitFolder, "mutants.yml")
	content, err := os.ReadFile(path)
	if err == nil {
		if _, localErr := os.Stat(local); localErr == nil {
			fmt.Fprintf(stderr, "mutants ignores %s, because %s exists\n", messagePath(root, local), messagePath(root, path))
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		path = local
		content, err = os.ReadFile(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return config{}, nil
	}
	if err != nil {
		return config{}, err
	}
	file := messagePath(root, path)
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return config{}, fmt.Errorf("read %s: %w", file, err)
	}
	if len(document.Content) == 0 {
		return config{}, nil
	}
	var loaded config
	if err := checkKeys(document.Content[0], reflect.TypeFor[config](), "", file); err != nil {
		return config{}, err
	}
	if err := document.Decode(&loaded); err != nil {
		return config{}, fmt.Errorf("read %s: %w", file, err)
	}
	return loaded, nil
}

func messagePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return path
	}
	return relative
}

func checkKeys(mapping *yaml.Node, settings reflect.Type, section, file string) error {
	keys := keysOf(settings)
	if mapping.Kind != yaml.MappingNode {
		if section != "" {
			return fmt.Errorf("%s in %s (line %d) must hold keys and values, and the keys are %s", section, file, mapping.Line, strings.Join(keys, ", "))
		}
		return fmt.Errorf("%s must hold keys and values, and the keys are %s", file, strings.Join(keys, ", "))
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		index := slices.Index(keys, key.Value)
		if index < 0 && section != "" {
			return fmt.Errorf("unknown key %s.%s in %s (line %d): the keys of %s are %s", section, key.Value, file, key.Line, section, strings.Join(keys, ", "))
		}
		if index < 0 {
			return fmt.Errorf("unknown key %s in %s (line %d): the keys are %s", key.Value, file, key.Line, strings.Join(keys, ", "))
		}
		if field := settings.Field(index).Type; field.Kind() == reflect.Struct && value.Tag != "!!null" {
			if err := checkKeys(value, field, key.Value, file); err != nil {
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

func (c config) goSettings(cmd *cli.Command, workers int) golang.Settings {
	return golang.Settings{
		Tags:          c.tags(cmd),
		BuildLimit:    cmd.Duration("build-limit"),
		Workers:       workers,
		ZeroFunctions: c.Go.ZeroFunctions,
		ExcludeTypes:  c.Go.ExcludeTypes,
	}
}

func (c config) pythonSettings(workers int) python.Settings {
	return python.Settings{Command: c.Python.Command, Workers: workers}
}
