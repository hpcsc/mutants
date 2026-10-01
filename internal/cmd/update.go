package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hpcsc/mutants/internal/progress"
	"github.com/hpcsc/mutants/internal/release"
	"github.com/hpcsc/mutants/internal/version"
	"github.com/mattn/go-isatty"
	"github.com/urfave/cli/v3"
)

func newUpdateCommand() *cli.Command {
	return &cli.Command{
		Name:  "update",
		Usage: "replace mutants with the latest release, or with the latest prerelease",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "prerelease", Usage: "install the latest prerelease, a build of main, in place of the latest release"},
			&cli.BoolFlag{Name: "check", Usage: "only report whether this build is the latest"},
			&cli.BoolFlag{Name: "force", Usage: "replace a build from a commit, which is not a release or a prerelease"},
		},
		Action: update,
	}
}

func update(ctx context.Context, cmd *cli.Command) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if executable, err = filepath.EvalSymlinks(executable); err != nil {
		return err
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	api := os.Getenv("GITHUB_API_URL")
	if api == "" {
		api = "https://api.github.com"
	}
	client := release.NewClient(&http.Client{Timeout: 2 * time.Minute}, api, releaseRepository, token)
	platform := runtime.GOOS + "-" + runtime.GOARCH
	updater := release.NewUpdater(client, version.Current(), platform, executable)
	channel, command := release.Releases, "mutants update"
	if cmd.Bool("prerelease") {
		channel, command = release.Prereleases, "mutants update --prerelease"
	}
	status := cmd.Root().ErrWriter
	fmt.Fprintf(status, "Finding the latest %s of %s…\n", channel, releaseRepository)
	check, err := updater.Check(ctx, channel)
	if errors.Is(err, release.ErrNoRelease) {
		return fmt.Errorf("found no %s of %s: it has none yet, or it is private and GITHUB_TOKEN is not set", channel, releaseRepository)
	}
	if err != nil {
		return err
	}

	out := cmd.Root().Writer
	switch {
	case !version.IsTagged(check.Current) && !cmd.Bool("force"):
		_, err = fmt.Fprintf(out, "mutants %s is a build from a commit. The latest %s is %s.\n"+
			"Run %s --force to replace this build with it.\n", check.Current, channel, check.Latest.Tag, command)
		return err
	case check.UpToDate:
		_, err = fmt.Fprintf(out, "mutants %s is the latest %s.\n", check.Current, channel)
		return err
	case cmd.Bool("check"):
		_, err = fmt.Fprintf(out, "mutants %s is available. This is %s. Run %s to install it.\n", check.Latest.Tag, check.Current, command)
		return err
	}
	download := progress.Start(status, isTerminal(status), fmt.Sprintf("Downloading mutants %s for %s", check.Latest.Tag, platform))
	err = updater.Install(ctx, check.Latest, download.Bytes)
	download.End()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Updated mutants from %s to %s at %s.\n", check.Current, check.Latest.Tag, executable)
	return err
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(f.Fd())
}
