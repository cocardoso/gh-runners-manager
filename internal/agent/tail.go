package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Tailer follows files matching glob patterns under dir and emits complete lines.
type Tailer struct {
	dir      string
	patterns map[string]string // glob (relative to dir) -> stream
	emit     func(stream, line string)
	offsets  map[string]int64
	partial  map[string]string
}

// NewTailer returns a Tailer.
func NewTailer(dir string, patterns map[string]string, emit func(stream, line string)) *Tailer {
	return &Tailer{dir: dir, patterns: patterns, emit: emit, offsets: map[string]int64{}, partial: map[string]string{}}
}

// Poll reads new content from every matching file.
func (t *Tailer) Poll() {
	globs := make([]string, 0, len(t.patterns))
	for g := range t.patterns {
		globs = append(globs, g)
	}
	sort.Strings(globs)
	for _, g := range globs {
		files, _ := filepath.Glob(filepath.Join(t.dir, g))
		sort.Strings(files)
		for _, f := range files {
			t.read(f, t.patterns[g])
		}
	}
}

func (t *Tailer) read(path, stream string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(t.offsets[path], io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, 8<<20))
	if err != nil || len(data) == 0 {
		return
	}
	t.offsets[path] += int64(len(data))
	text := t.partial[path] + string(data)
	lines := strings.Split(text, "\n")
	t.partial[path] = lines[len(lines)-1]
	for _, l := range lines[:len(lines)-1] {
		t.emit(stream, strings.TrimRight(l, "\r"))
	}
}

// Run polls every interval until ctx ends.
func (t *Tailer) Run(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.Poll()
		}
	}
}
