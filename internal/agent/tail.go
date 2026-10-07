package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tailer follows files matching glob patterns under dir and emits complete lines.
type Tailer struct {
	dir      string
	patterns map[string]string // glob (relative to dir) -> stream
	emit     func(stream, line string)
	// Accept, when set, decides whether a file is read now. A file that is not
	// accepted keeps its offset and is read from the start once accepted.
	Accept  func(stream, path string) bool
	offsets map[string]int64
	partial map[string]string
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
		sort.Slice(files, func(i, j int) bool { return naturalLess(files[i], files[j]) })
		for _, f := range files {
			if t.Accept != nil && !t.Accept(t.patterns[g], f) {
				continue
			}
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

var trailingNumber = regexp.MustCompile(`^(.*?)(\d+)(\.log)$`)

// naturalLess orders "x_2.log" before "x_10.log".
func naturalLess(a, b string) bool {
	ma, mb := trailingNumber.FindStringSubmatch(a), trailingNumber.FindStringSubmatch(b)
	if ma != nil && mb != nil && ma[1] == mb[1] {
		na, _ := strconv.Atoi(ma[2])
		nb, _ := strconv.Atoi(mb[2])
		return na < nb
	}
	return a < b
}
