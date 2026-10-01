//go:build unit

package version_test

import (
	"runtime/debug"
	"testing"

	"github.com/hpcsc/mutants/internal/version"
	"github.com/stretchr/testify/require"
)

func TestVersion(t *testing.T) {
	build := func(moduleVersion, revision, modified string) *debug.BuildInfo {
		info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/hpcsc/mutants", Version: moduleVersion}}
		if revision != "" {
			info.Settings = append(info.Settings,
				debug.BuildSetting{Key: "vcs.revision", Value: revision},
				debug.BuildSetting{Key: "vcs.modified", Value: modified})
		}
		return info
	}

	t.Run("from build", func(t *testing.T) {
		t.Run("a release build reports the tag the release set", func(t *testing.T) {
			require.Equal(t, "v0.2.0", version.FromBuild("v0.2.0", build("v0.0.0-20260911114137-875558850016", "875558850016133e", "false")))
		})

		t.Run("a build of a tagged commit reports the tag", func(t *testing.T) {
			require.Equal(t, "v0.1.0", version.FromBuild("", build("v0.1.0", "875558850016133e", "false")))
		})

		t.Run("a build of an untagged commit reports the short commit sha", func(t *testing.T) {
			require.Equal(t, "8755588", version.FromBuild("", build("v0.1.1-0.20260911114137-875558850016", "875558850016133e", "false")))
		})

		t.Run("a build with uncommitted changes marks the sha dirty", func(t *testing.T) {
			require.Equal(t, "8755588-dirty", version.FromBuild("", build("v0.1.0+dirty", "875558850016133e", "true")))
		})

		t.Run("a build with no version control information is unknown", func(t *testing.T) {
			require.Equal(t, version.Unknown, version.FromBuild("", build("(devel)", "", "")))
		})
	})

	t.Run("is tagged", func(t *testing.T) {
		t.Run("a release, a hand-made prerelease and a prerelease from main are tagged", func(t *testing.T) {
			require.True(t, version.IsTagged("v1.2.3"))
			require.True(t, version.IsTagged("v1.2.3-rc.1"))
			require.True(t, version.IsTagged("v1.2.4-42.g4829f92"))
		})

		t.Run("a commit sha or a pseudo-version is not tagged", func(t *testing.T) {
			require.False(t, version.IsTagged("8755588"))
			require.False(t, version.IsTagged("8755588-dirty"))
			require.False(t, version.IsTagged("v0.0.0-20260911114137-875558850016"))
		})
	})
}
