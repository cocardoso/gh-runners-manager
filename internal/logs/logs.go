// Package logs stores the raw log streams of environments as append-only files
// and lets readers follow them live.
package logs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Streams are the log stream names of an environment (spec §6).
var Streams = []string{"control-plane", "runtime", "agent", "runner", "job", "metrics"}

// ValidStream reports whether name is a known stream.
func ValidStream(name string) bool { return slices.Contains(Streams, name) }

var envIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Line is one line sent by a producer, with its per-stream sequence number.
type Line struct {
	Seq  int64
	Time time.Time
	Text string
}

// Entry is one stored line and the byte offset where it starts.
type Entry struct {
	Offset int64     `json:"offset"`
	Time   time.Time `json:"time"`
	Text   string    `json:"text"`
}

// Store writes stream files under dir and their metadata in the database.
type Store struct {
	dir string
	db  *store.Store

	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	notifies map[string]chan struct{}
}

// New returns a Store rooted at dir.
func New(dir string, db *store.Store) *Store {
	return &Store{dir: dir, db: db, locks: map[string]*sync.Mutex{}, notifies: map[string]chan struct{}{}}
}

func validate(envID, stream string) error {
	if !envIDPattern.MatchString(envID) {
		return fmt.Errorf("logs: invalid environment id %q", envID)
	}
	if !ValidStream(stream) {
		return fmt.Errorf("logs: unknown stream %q", stream)
	}
	return nil
}

func (s *Store) path(envID, stream string) string {
	return filepath.Join(s.dir, envID, stream+".log")
}

func (s *Store) lock(key string) func() {
	s.mu.Lock()
	m, ok := s.locks[key]
	if !ok {
		m = &sync.Mutex{}
		s.locks[key] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// waiter returns a channel closed at the next append to key.
func (s *Store) waiter(key string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.notifies[key]
	if !ok {
		ch = make(chan struct{})
		s.notifies[key] = ch
	}
	return ch
}

func (s *Store) notify(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.notifies[key]; ok {
		close(ch)
		delete(s.notifies, key)
	}
}

func sanitize(text string) string {
	text = strings.ReplaceAll(text, "\r", "")
	return strings.ReplaceAll(text, "\n", `\n`)
}

// Append stores the lines whose sequence is newer than the stream's last one and
// returns how many were accepted. Replayed lines are dropped.
func (s *Store) Append(ctx context.Context, envID, stream string, lines []Line) (int, error) {
	if err := validate(envID, stream); err != nil {
		return 0, err
	}
	key := envID + "/" + stream
	unlock := s.lock(key)
	defer unlock()

	meta, err := s.db.GetLogStream(ctx, envID, stream)
	if errors.Is(err, store.ErrNotFound) {
		meta = store.LogStream{EnvironmentID: envID, Stream: stream, Path: s.path(envID, stream)}
	} else if err != nil {
		return 0, err
	}
	sorted := slices.Clone(lines)
	slices.SortStableFunc(sorted, func(a, b Line) int { return int(a.Seq - b.Seq) })

	var buf strings.Builder
	accepted := 0
	for _, l := range sorted {
		if l.Seq <= meta.LastSeq {
			continue
		}
		at := l.Time.UTC()
		if l.Time.IsZero() {
			at = time.Now().UTC()
		}
		buf.WriteString(at.Format(time.RFC3339Nano))
		buf.WriteByte('\t')
		buf.WriteString(sanitize(l.Text))
		buf.WriteByte('\n')
		meta.LastSeq = l.Seq
		meta.Lines++
		if meta.FirstAt.IsZero() {
			meta.FirstAt = at
		}
		meta.LastAt = at
		accepted++
	}
	if accepted == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(meta.Path), 0o750); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(meta.Path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	// Drop bytes the metadata does not know about (a write that was never recorded),
	// so the file and LastSeq always agree.
	if err := f.Truncate(meta.Bytes); err != nil {
		_ = f.Close()
		return 0, err
	}
	if _, err := f.Seek(meta.Bytes, io.SeekStart); err != nil {
		_ = f.Close()
		return 0, err
	}
	n, werr := f.WriteString(buf.String())
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return 0, err
	}
	meta.Bytes += int64(n)
	if err := s.db.UpsertLogStream(ctx, meta); err != nil {
		return 0, err
	}
	s.notify(key)
	return accepted, nil
}

// Write appends one line produced by the control plane, assigning the next sequence.
func (s *Store) Write(ctx context.Context, envID, stream, text string, at time.Time) error {
	if err := validate(envID, stream); err != nil {
		return err
	}
	unlock := s.lock(envID + "/" + stream + "#seq")
	defer unlock()
	var last int64
	if meta, err := s.db.GetLogStream(ctx, envID, stream); err == nil {
		last = meta.LastSeq
	}
	_, err := s.Append(ctx, envID, stream, []Line{{Seq: last + 1, Time: at, Text: text}})
	return err
}

// Read returns up to max entries starting at byte offset, and the offset after them.
// A stream that does not exist yet reads as empty.
func (s *Store) Read(ctx context.Context, envID, stream string, offset int64, max int) ([]Entry, int64, error) {
	if err := validate(envID, stream); err != nil {
		return nil, offset, err
	}
	f, err := os.Open(s.path(envID, stream))
	if errors.Is(err, os.ErrNotExist) {
		return nil, offset, nil
	}
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	r := bufio.NewReaderSize(f, 64*1024)
	var out []Entry
	pos := offset
	for len(out) < max {
		line, err := r.ReadString('\n')
		if err != nil {
			break // EOF or a partial line: stop before it
		}
		e := Entry{Offset: pos}
		pos += int64(len(line))
		ts, text, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
		e.Time, _ = time.Parse(time.RFC3339Nano, ts)
		e.Text = text
		out = append(out, e)
	}
	return out, pos, nil
}

// ReadBefore returns up to max entries that end at or before the byte offset before
// (an entry offset, or -1 for the end of the stream), and the offset where they end.
func (s *Store) ReadBefore(ctx context.Context, envID, stream string, before int64, max int) ([]Entry, int64, error) {
	if err := validate(envID, stream); err != nil {
		return nil, 0, err
	}
	end := int64(0)
	if meta, err := s.db.GetLogStream(ctx, envID, stream); err == nil {
		end = meta.Bytes
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, 0, err
	}
	if before >= 0 && before < end {
		end = before
	}
	if end == 0 || max <= 0 {
		return nil, end, nil
	}
	f, err := os.Open(s.path(envID, stream))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, end, err
	}
	defer f.Close()
	// Read backwards in chunks until the buffer holds max complete lines.
	const chunk = 64 * 1024
	pos := end
	var buf []byte
	for pos > 0 && bytes.Count(buf, []byte{'\n'}) <= max {
		n := min(int64(chunk), pos)
		pos -= n
		part := make([]byte, n)
		if _, err := f.ReadAt(part, pos); err != nil {
			return nil, end, err
		}
		buf = append(part, buf...)
	}
	start := pos
	if pos > 0 { // drop the partial line at the front
		i := bytes.IndexByte(buf, '\n')
		buf, start = buf[i+1:], pos+int64(i+1)
	}
	var out []Entry
	for off := start; len(buf) > 0; {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		ts, text, _ := strings.Cut(string(buf[:i]), "\t")
		e := Entry{Offset: off, Text: text}
		e.Time, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
		off += int64(i + 1)
		buf = buf[i+1:]
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out, end, nil
}

// Follow emits entries from offset, then new entries as they are appended, until ctx ends.
func (s *Store) Follow(ctx context.Context, envID, stream string, offset int64) <-chan Entry {
	ch := make(chan Entry, 256)
	go func() {
		defer close(ch)
		key := envID + "/" + stream
		for {
			wait := s.waiter(key) // taken before reading so no append is missed
			entries, next, err := s.Read(ctx, envID, stream, offset, 1000)
			if err != nil {
				return
			}
			for _, e := range entries {
				select {
				case ch <- e:
				case <-ctx.Done():
					return
				}
			}
			offset = next
			if len(entries) > 0 {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-wait:
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return ch
}
