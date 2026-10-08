package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(t.TempDir(), db)
}

func lines(from, to int) []Line {
	var out []Line
	for i := from; i <= to; i++ {
		out = append(out, Line{Seq: int64(i), Time: time.Unix(int64(i), 0), Text: "line " + string(rune('0'+i))})
	}
	return out
}

func TestAppendDedupsBySeq(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if n, err := s.Append(ctx, "env1", "job", lines(1, 3)); err != nil || n != 3 {
		t.Fatalf("first append = %d, %v", n, err)
	}
	if n, err := s.Append(ctx, "env1", "job", lines(2, 5)); err != nil || n != 2 {
		t.Fatalf("second append accepted %d, %v; want 2 (seq 4 and 5)", n, err)
	}
	entries, _, err := s.Read(ctx, "env1", "job", 0, 100)
	if err != nil || len(entries) != 5 || entries[4].Text != "line 5" {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
}

func TestWriteAssignsSequence(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_ = s.Write(ctx, "env1", "control-plane", "first", time.Now())
	_ = s.Write(ctx, "env1", "control-plane", "second\nwith newline", time.Now())
	entries, _, _ := s.Read(ctx, "env1", "control-plane", 0, 10)
	if len(entries) != 2 || entries[1].Text != `second\nwith newline` {
		t.Fatalf("entries = %+v, newlines must be escaped to keep one entry per line", entries)
	}
}

func TestReadWithOffsets(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Append(ctx, "env1", "runner", lines(1, 5))
	first, next, err := s.Read(ctx, "env1", "runner", 0, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	rest, end, _ := s.Read(ctx, "env1", "runner", next, 100)
	if len(rest) != 3 || rest[0].Text != "line 3" || rest[0].Offset != next {
		t.Fatalf("rest = %+v", rest)
	}
	none, same, _ := s.Read(ctx, "env1", "runner", end, 100)
	if len(none) != 0 || same != end {
		t.Fatalf("read at end = %+v next %d", none, same)
	}
	missing, n, err := s.Read(ctx, "env1", "metrics", 0, 10)
	if err != nil || len(missing) != 0 || n != 0 {
		t.Fatalf("missing stream = %+v %d %v, want empty", missing, n, err)
	}
}

func TestFollowDeliversBacklogThenLive(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _ = s.Append(ctx, "env1", "job", lines(1, 2))
	ch := s.Follow(ctx, "env1", "job", 0)
	got := func() Entry {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatal("channel closed early")
			}
			return e
		case <-time.After(3 * time.Second):
			t.Fatal("timed out")
			return Entry{}
		}
	}
	if got().Text != "line 1" || got().Text != "line 2" {
		t.Fatal("backlog out of order")
	}
	_, _ = s.Append(ctx, "env1", "job", lines(3, 3))
	if e := got(); e.Text != "line 3" {
		t.Fatalf("live entry = %+v", e)
	}
	cancel()
	for range ch {
	}
}

func TestRejectsUnknownStreamAndPathTraversal(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, env := range []string{"../etc", "a/b", "", "UPPER"} {
		if _, err := s.Append(ctx, env, "job", lines(1, 1)); err == nil {
			t.Errorf("environment %q accepted", env)
		}
	}
	if _, err := s.Append(ctx, "env1", "../../passwd", lines(1, 1)); err == nil {
		t.Error("unknown stream accepted")
	}
	if !ValidStream("metrics") || ValidStream("bogus") {
		t.Error("ValidStream is wrong")
	}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") {
			t.Errorf("unexpected entry %q", e.Name())
		}
	}
}

// A crash (or a cancelled request) between the file write and the metadata update
// leaves bytes in the file that the metadata does not know about; the next append
// must not keep them, or replayed lines would appear twice.
func TestAppendDiscardsBytesNotRecordedInMetadata(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Append(ctx, "env1", "job", lines(1, 2))
	f, _ := os.OpenFile(s.path("env1", "job"), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("2026-01-01T00:00:03Z\tline 3\n") // written, metadata never updated
	_ = f.Close()
	if n, err := s.Append(ctx, "env1", "job", lines(3, 3)); err != nil || n != 1 {
		t.Fatalf("replay append = %d, %v", n, err)
	}
	entries, _, _ := s.Read(ctx, "env1", "job", 0, 100)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (no duplicate of line 3)", len(entries))
	}
}

func TestReadBeforeReturnsTheLinesEndingBeforeAnOffset(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var many []Line
	for i := 1; i <= 3000; i++ {
		many = append(many, Line{Seq: int64(i), Time: time.Unix(int64(i), 0), Text: fmt.Sprintf("line %d", i)})
	}
	_, _ = s.Append(ctx, "env1", "job", many)
	tail, end, err := s.ReadBefore(ctx, "env1", "job", -1, 2)
	if err != nil || len(tail) != 2 || tail[0].Text != "line 2999" || tail[1].Text != "line 3000" {
		t.Fatalf("tail = %+v, %v", tail, err)
	}
	if rest, _, _ := s.Read(ctx, "env1", "job", end, 10); len(rest) != 0 {
		t.Fatalf("end offset %d is not the end of the stream: %+v", end, rest)
	}
	earlier, next, _ := s.ReadBefore(ctx, "env1", "job", tail[0].Offset, 1500)
	if len(earlier) != 1500 || earlier[0].Text != "line 1499" || earlier[1499].Text != "line 2998" || next != tail[0].Offset {
		t.Fatalf("earlier = %d lines from %q to %q, next %d", len(earlier), earlier[0].Text, earlier[len(earlier)-1].Text, next)
	}
	first, _, _ := s.ReadBefore(ctx, "env1", "job", earlier[0].Offset, 5000)
	if len(first) != 1498 || first[0].Offset != 0 || first[0].Text != "line 1" {
		t.Fatalf("first = %d lines starting %+v", len(first), first[0])
	}
	if none, n, err := s.ReadBefore(ctx, "env1", "metrics", -1, 10); err != nil || len(none) != 0 || n != 0 {
		t.Fatalf("missing stream = %+v %d %v", none, n, err)
	}
}

func TestReadBeforeEndOffsetsAndFirstLine(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Append(ctx, "env1", "job", lines(1, 5))
	all, end, _ := s.Read(ctx, "env1", "job", 0, 10)
	// A before inside a line returns the lines that end before it, and where they end.
	mid := all[3].Offset + 3
	got, next, err := s.ReadBefore(ctx, "env1", "job", mid, 10)
	if err != nil || len(got) != 3 || next != all[3].Offset {
		t.Fatalf("mid-line: %d entries, next %d, want 3 and %d", len(got), next, all[3].Offset)
	}
	// From the end, the first entry's absolute line number is known.
	tail, err := s.ReadTail(ctx, "env1", "job", 2)
	if err != nil || len(tail.Entries) != 2 || tail.FirstLine != 4 || tail.Next != end {
		t.Fatalf("tail = %+v, %v; want lines 4-5 and next %d", tail, err, end)
	}
}

func TestReadBeforeMissingFileReportsMetadataEnd(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, _ = s.Append(ctx, "env1", "job", lines(1, 2))
	_, end, _ := s.Read(ctx, "env1", "job", 0, 10)
	if err := os.RemoveAll(filepath.Dir(s.path("env1", "job"))); err != nil {
		t.Fatal(err)
	}
	if got, next, err := s.ReadBefore(ctx, "env1", "job", -1, 10); err != nil || len(got) != 0 || next != end {
		t.Fatalf("missing file: %d entries, next %d (%v); want 0 and %d", len(got), next, err, end)
	}
}

func TestRemoveEnvironmentDeletesItsLogs(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Write(ctx, "env1", "runner", "hello", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveEnvironment("env1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "env1")); !os.IsNotExist(err) {
		t.Fatal("log directory kept")
	}
	if err := s.RemoveEnvironment("env1"); err != nil {
		t.Fatalf("a missing directory is fine: %v", err)
	}
	if err := s.RemoveEnvironment("../x"); err == nil {
		t.Fatal("an invalid id must be refused")
	}
}

func TestLateLinesForARemovedEnvironmentAreDropped(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Write(ctx, "env1", "runner", "first", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveEnvironment("env1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, "env1", "runner", []Line{{Seq: 9, Text: "late"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "env1")); !os.IsNotExist(err) {
		t.Fatal("a late append recreated the deleted logs")
	}
	if streams, _ := s.db.ListLogStreams(ctx, "env1"); len(streams) != 0 {
		t.Fatal("a late append recreated the stream's row")
	}
}
