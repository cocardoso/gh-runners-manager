package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// Production upgrades a database with an active (bootstrap) template and built versions.
func TestProfilesMigrationKeepsTheActiveTemplate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := fs.Sub(migrations, "migrations")
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO templates (id, state, vmid, trigger, created_at, updated_at) VALUES ('boot', 'ready', 949, 'bootstrap', 1, 1)`,
		`INSERT INTO templates (id, state, vmid, runtime_ref, trigger, created_at, updated_at) VALUES ('built', 'active', 952, '952/built', 'layer', 2, 2)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	s := &Store{db: db}
	a, err := s.ActiveTemplate(ctx, DefaultProfile)
	if err != nil || a.ID != "built" || a.Profile != DefaultProfile {
		t.Fatalf("active after the upgrade = %+v, %v", a, err)
	}
	// Down works with one active version per profile.
	if err := s.CreateTemplate(ctx, Template{ID: "lean1", State: TemplateActive, VMID: 953, Profile: "lean"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 5); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	_ = db.Close()
}
