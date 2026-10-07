// Package ids generates identifiers.
package ids

import (
	"strings"

	"github.com/oklog/ulid/v2"
)

// NewEnvironmentID returns a new, time-ordered, lowercase environment identifier.
// Lowercase keeps it valid in Proxmox tags and hostnames.
func NewEnvironmentID() string {
	return strings.ToLower(ulid.Make().String())
}
