package secrets_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/secrets"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func TestSealOpenRoundTripBindsTheName(t *testing.T) {
	key, err := secrets.LoadOrCreateKey(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal("github/home", []byte("ghp_x"))
	if err != nil || bytes.Contains(sealed, []byte("ghp_x")) {
		t.Fatalf("sealed %q, %v", sealed, err)
	}
	if got, err := b.Open("github/home", sealed); err != nil || string(got) != "ghp_x" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := b.Open("github/other", sealed); err == nil {
		t.Fatal("a value sealed for one name must not open under another")
	}
	again, _ := b.Seal("github/home", []byte("ghp_x"))
	if bytes.Equal(again, sealed) {
		t.Fatal("sealing twice must use a fresh nonce")
	}
}

func TestKeyFileIsCreatedOnceWith0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret.key")
	k1, err := secrets.LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := secrets.LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if !bytes.Equal(k1, k2) || len(k1) != 32 || st.Mode().Perm() != 0o600 {
		t.Fatalf("key %d bytes, same %v, mode %v", len(k1), bytes.Equal(k1, k2), st.Mode())
	}
}

func TestKeyFileWithLoosePermissionsOrWrongSizeIsRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret.key")
	if _, err := secrets.LoadOrCreateKey(p); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, 0o644)
	if _, err := secrets.LoadOrCreateKey(p); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("loose permissions: %v", err)
	}
	short := filepath.Join(t.TempDir(), "short.key")
	_ = os.WriteFile(short, []byte("abc"), 0o600)
	if _, err := secrets.LoadOrCreateKey(short); err == nil {
		t.Fatal("a 3-byte key must be refused")
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestVaultStoresSealedValues(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	v, err := secrets.OpenVault(ctx, db, filepath.Join(t.TempDir(), "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := v.Get(ctx, "github/home"); ok || err != nil {
		t.Fatalf("missing = %v, %v", ok, err)
	}
	if err := v.Set(ctx, "github/home", "ghp_x"); err != nil {
		t.Fatal(err)
	}
	raw, _ := db.GetSecret(ctx, "github/home")
	if bytes.Contains(raw, []byte("ghp_x")) {
		t.Fatal("the stored value must be sealed")
	}
	if got, ok, err := v.Get(ctx, "github/home"); got != "ghp_x" || !ok || err != nil {
		t.Fatalf("get = %q %v %v", got, ok, err)
	}
	if err := v.Delete(ctx, "github/home"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := v.Get(ctx, "github/home"); ok {
		t.Fatal("deleted value still there")
	}
}

func TestOpenWithAWrongKeyFailsLoudly(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	dir := t.TempDir()
	v, err := secrets.OpenVault(ctx, db, filepath.Join(dir, "a.key"))
	if err != nil {
		t.Fatal(err)
	}
	_ = v.Set(ctx, "github/home", "ghp_x")
	if _, err := secrets.OpenVault(ctx, db, filepath.Join(dir, "a.key")); err != nil {
		t.Fatalf("same key again: %v", err)
	}
	_, err = secrets.OpenVault(ctx, db, filepath.Join(dir, "b.key")) // a fresh, different key
	if err == nil || !strings.Contains(err.Error(), "b.key") {
		t.Fatalf("err = %v, want a refusal naming the key file", err)
	}
}
