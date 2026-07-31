// Package version exposes the CLI version and the identifiers derived from it.
package version

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
)

// Version is the CLI version: the release tag stamped by the release build
// (-ldflags "-X github.com/Flagsmith/flagsmith-cli/v2/internal/version.Version=v1.2.3"),
// else resolved from build info.
var Version = "dev"

func init() {
	Version = resolve(Version)
}

// resolve upgrades an unstamped version from the build info the toolchain
// embeds: a `go install module@tag` build carries the module version, a source
// build carries the VCS revision.
func resolve(v string) string {
	if v != "dev" {
		return v // stamped by the release build
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v
	}
	if mv := info.Main.Version; mv != "" && mv != "(devel)" {
		return mv
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return "dev (" + s.Value[:7] + ")"
		}
	}
	return v
}

// releaseTag is exactly a release version — not a pre-release and not a
// go-install pseudo-version, whose refs may not exist as tags.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// IsRelease reports whether v may pin URLs to a tag of the same name.
func IsRelease(v string) bool {
	return releaseTag.MatchString(v)
}

// UserAgent identifies the CLI on outbound HTTP requests.
func UserAgent() string {
	return fmt.Sprintf("flagsmith-cli/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
}
