package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFailedSinceUsesTheFailedAtIndex(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT `+envColumns+` FROM environments WHERE failed_at > 0 AND failed_at >= ?`, ms(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "environments_failed_at") {
		t.Fatalf("plan = %q, want the environments_failed_at index", plan)
	}
}
