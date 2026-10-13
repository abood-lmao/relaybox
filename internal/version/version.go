// Package version holds build information injected at link time.
package version

import (
	"fmt"
	"runtime"
)

// These values are overridden with -ldflags "-X" during release builds.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns a one-line description of the build.
func String() string {
	return fmt.Sprintf("relaybox %s (commit %s, built %s, %s %s/%s)",
		Version, Commit, Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
