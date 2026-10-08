//go:build !linux && !darwin

package agent

import (
	"os"
	"time"
)

// lastUse falls back to the modification time where access times are not read.
func lastUse(info os.FileInfo) time.Time { return info.ModTime() }
