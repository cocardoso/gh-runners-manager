package logs

import (
	"context"
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
