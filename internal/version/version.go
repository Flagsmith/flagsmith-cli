// Package version exposes the CLI version and identifiers derived from it, so
// every layer (Admin API client, OAuth, raw api command) reports the same build.
package version

import (
	"fmt"
	"runtime"
)

// Version is the CLI version tag, stamped by the release build (overridable via
// -ldflags "-X github.com/Flagsmith/flagsmith-cli/internal/version.Version=...").
var Version = "feat/cli-v2"

// UserAgent identifies the CLI (version + platform) on outbound HTTP requests,
// so the API can attribute traffic and correlate issues to a release.
func UserAgent() string {
	return fmt.Sprintf("flagsmith-cli/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
}
