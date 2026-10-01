package version

import (
	"regexp"
	"runtime/debug"
)

// the release build sets releaseTag through -ldflags
var releaseTag string

var (
	tagVersion    = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}$`)
)

const Unknown = "unknown"

func Current() string {
	info, _ := debug.ReadBuildInfo()
	return FromBuild(releaseTag, info)
}

func FromBuild(tag string, info *debug.BuildInfo) string {
	if tag != "" {
		return tag
	}
	if info == nil {
		return Unknown
	}
	if IsTagged(info.Main.Version) {
		return info.Main.Version
	}
	revision, modified := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return Unknown
	}
	short := revision[:min(7, len(revision))]
	if modified {
		short += "-dirty"
	}
	return short
}

func IsTagged(v string) bool {
	return tagVersion.MatchString(v) && !pseudoVersion.MatchString(v)
}
