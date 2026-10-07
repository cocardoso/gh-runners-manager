package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadCgroup returns the cgroup v2 CPU usage (µs) and current memory (bytes).
func ReadCgroup(dir string) (cpuUsec, memBytes int64, err error) {
	f, err := os.Open(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	found := false
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "usage_usec "); ok {
			cpuUsec, err = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			found = err == nil
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("agent: usage_usec not found in %s", dir)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if err != nil {
		return 0, 0, err
	}
	memBytes, err = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return cpuUsec, memBytes, err
}
