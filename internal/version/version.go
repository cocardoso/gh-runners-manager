// Package version holds build metadata injected with -ldflags.
package version

import "fmt"

// Version and Commit are overridden at build time, for example:
//
//	-X github.com/cocardoso/gh-runners-manager/internal/version.Version=v0.1.0
var (
	Version = "dev"
	Commit  = "none"
)

// String returns a human-readable version line.
func String() string {
	return fmt.Sprintf("ghrm %s (%s)", Version, Commit)
}
