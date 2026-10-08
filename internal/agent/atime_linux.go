package agent

import (
	"os"
	"syscall"
	"time"
)

// lastUse is the later of a file's access and modification times.
func lastUse(info os.FileInfo) time.Time {
	t := info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if a := time.Unix(st.Atim.Unix()); a.After(t) {
			t = a
		}
	}
	return t
}
