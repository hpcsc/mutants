package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hpcsc/mutants/internal/diff"
	"github.com/hpcsc/mutants/internal/language/golang"
	"github.com/urfave/cli/v3"
)

func newConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "work with .mutants.yml, the settings of mutants for a repository",
		Commands: []*cli.Command{{
			Name:  "init",
			Usage: "write .mutants.yml at the root of the repository, with its base and the build tags of its tests",
			Description: "mutants config init reads the default branch of origin and the build tags of the test files, and\n" +
				"writes each other setting as a comment. It does not replace a .mutants.yml that exists.",
			Action: initConfig,
		}},
	}
}

func initConfig(ctx context.Context, cmd *cli.Command) error {
	repository, err := diff.Open(ctx, ".")
	if err != nil {
		return cli.Exit(err, exitUsage)
	}
	base, _ := repository.OriginHead(ctx)
	tags, err := golang.TagsOfTests(ctx, repository.Root())
	if err != nil {
		return cli.Exit(fmt.Errorf("read the build tags of the tests: %w", err), exitUsage)
	}
	path := filepath.Join(repository.Root(), ".mutants.yml")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return cli.Exit(fmt.Sprintf("%s exists, and mutants config init does not replace it", path), exitUsage)
	}
	if err != nil {
		return cli.Exit(err, exitUsage)
	}
	if _, err := file.WriteString(configTemplate(base, tags)); err != nil {
		file.Close()
		return cli.Exit(err, exitUsage)
	}
	if err := file.Close(); err != nil {
		return cli.Exit(err, exitUsage)
	}
	_, err = fmt.Fprintf(cmd.Root().Writer, "wrote %s\n", path)
	return err
}

// configTemplate sets tags only when the tests have one tag, because a second tag, such as integration, can
// need a service that the machine does not have.
func configTemplate(base string, tags map[string]int) string {
	var text strings.Builder
	text.WriteString("# The settings of mutants for this repository. A flag of mutants run wins over a setting here.\n\n")

	text.WriteString("# mutants compares the work tree with the merge base of HEAD and this base.\n")
	if base != "" {
		fmt.Fprintf(&text, "base: %s\n\n", base)
	} else {
		text.WriteString("# The clone does not know the default branch of origin, so the base is the default.\n# base: origin/HEAD\n\n")
	}

	text.WriteString("# The number of mutants that run at the same time.\n# workers: 4\n\n")
	text.WriteString("# The operators: -NAME takes one out, +NAME adds one, NAME runs only the named ones, and none runs\n" +
		"# no operator. mutants operators lists them.\n# operators: [-NAMED_VALUE_REMOVE]\n\n")
	text.WriteString("# The files that get no mutant, as globs from the root of the repository.\n" +
		"# exclude: [\"**/*_gen.go\", \"vendor/**\"]\n\n")
	text.WriteString("# Also find the changed statements that no test of a changed caller runs.\n# caller_gaps: false\n\n")

	text.WriteString("# The settings of Go.\ngo:\n")
	text.WriteString("  # A test file with a build tag runs only when tags names the tag. Without the tag, the mutants of\n" +
		"  # its package are NOT COVERED.\n")
	names := slices.Sorted(maps.Keys(tags))
	switch len(names) {
	case 0:
		text.WriteString("  # No test file of this repository needs a build tag.\n  # tags: []\n\n")
	case 1:
		fmt.Fprintf(&text, "  # The test files need this tag:\n  #   %s: %s\n", names[0], files(tags[names[0]]))
		fmt.Fprintf(&text, "  tags: [%s]\n\n", names[0])
	default:
		text.WriteString("  # The test files need these tags:\n")
		for _, name := range names {
			fmt.Fprintf(&text, "  #   %s: %s\n", name, files(tags[name]))
		}
		text.WriteString("  # Name each tag whose tests run on this machine with no other service, such as a database.\n")
		fmt.Fprintf(&text, "  # tags: [%s]\n\n", strings.Join(names, ", "))
	}
	text.WriteString("  # The functions that return the zero value of their type, as package.Function with the name of\n" +
		"  # the package. NAMED_VALUE_REMOVE skips a field whose value is a call of one of them, and RETURN_EMPTY\n" +
		"  # skips a struct literal whose fields are all such calls.\n  # zero_functions: [maybe.None]\n\n")

	text.WriteString("# The settings of Python.\npython:\n")
	text.WriteString("  # The command that starts the Python of a project, in the folder of the project. The default is\n" +
		"  # .venv/bin/python of the project when it exists, and python3 when it does not.\n  # command: [uv, run, python]\n")
	return text.String()
}

func files(count int) string {
	if count == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", count)
}
