package settings

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/secrets"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type env struct {
	db    *store.Store
	vault *secrets.Vault
	cfg   *config.Config
}

func newEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	v, err := secrets.OpenVault(ctx, db, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		GitHub: config.GitHub{Credentials: []config.Credential{{Name: "file-cred", Token: "github_pat_file"}}},
		ScaleSets: []config.ScaleSet{{Name: "file-ss", URL: "https://github.com/o/r", Credential: "file-cred",
			RunnerGroup: "default", MaxConcurrent: 2, Cores: 2, MemoryMB: 4096}},
	}
	cfg.Capacity.ApplyDefaults()
	return env{db: db, vault: v, cfg: cfg}
}

func (e env) registry(t *testing.T) *Registry {
	t.Helper()
	r, err := New(context.Background(), e.cfg, e.db, e.vault)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegistryMergesFileAndUI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	if err := r.PutCredential(ctx, "ui-cred", "github_pat_ui"); err != nil {
		t.Fatal(err)
	}
	if err := r.PutScaleSet(ctx, config.ScaleSet{Name: "ui-ss", URL: "https://github.com/o", Credential: "ui-cred"}); err != nil {
		t.Fatal(err)
	}
	// A restart reads the UI entries back.
	r = e.registry(t)
	creds := r.Credentials()
	if len(creds) != 2 || creds[0].Name != "file-cred" || creds[0].Source != SourceFile || creds[1].Name != "ui-cred" || creds[1].Source != SourceUI {
		t.Fatalf("credentials = %+v", creds)
	}
	if c, ok := r.Credential("ui-cred"); !ok || c.Token != "github_pat_ui" {
		t.Fatalf("ui credential = %+v %v", c, ok)
	}
	ss, ok := r.ScaleSet("ui-ss")
	if !ok || ss.Source != SourceUI || ss.MaxConcurrent != 2 || ss.Cores != 2 || ss.MemoryMB != 4096 || ss.RunnerGroup != "default" {
		t.Fatalf("ui scale set = %+v %v; want the defaults applied", ss, ok)
	}
	if names := r.ScaleSetConfigs(); len(names) != 2 || names[0].Name != "file-ss" || names[1].Name != "ui-ss" {
		t.Fatalf("scale sets = %+v", names)
	}
}

func TestFileEntriesAreReadOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	if err := r.PutCredential(ctx, "file-cred", "x"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("put file credential: %v", err)
	}
	if err := r.DeleteCredential(ctx, "file-cred"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("delete file credential: %v", err)
	}
	if err := r.PutScaleSet(ctx, config.ScaleSet{Name: "file-ss", URL: "https://github.com/o", Credential: "file-cred"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("put file scale set: %v", err)
	}
	if err := r.DeleteScaleSet(ctx, "file-ss"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("delete file scale set: %v", err)
	}
	if err := r.DeleteScaleSet(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete unknown: %v", err)
	}
}

func TestCredentialInUseCannotBeDeleted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	_ = r.PutCredential(ctx, "ui-cred", "github_pat_ui")
	_ = r.PutScaleSet(ctx, config.ScaleSet{Name: "ui-ss", URL: "https://github.com/o", Credential: "ui-cred"})
	if err := r.DeleteCredential(ctx, "ui-cred"); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete in use: %v", err)
	}
	if users := r.CredentialUsers("ui-cred"); len(users) != 1 || users[0] != "ui-ss" {
		t.Fatalf("users = %v", users)
	}
	_ = r.DeleteScaleSet(ctx, "ui-ss")
	if err := r.DeleteCredential(ctx, "ui-cred"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Credential("ui-cred"); ok {
		t.Fatal("deleted credential still there")
	}
	if _, ok, _ := e.vault.Get(ctx, "github/ui-cred"); ok {
		t.Fatal("the token must leave the vault too")
	}
}

func TestCredentialTokenIsSealedAtRest(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	_ = r.PutCredential(ctx, "ui-cred", "github_pat_secret_value")
	raw, err := e.db.GetSecret(ctx, "github/ui-cred")
	if err != nil || len(raw) == 0 || bytes.Contains(raw, []byte("github_pat_secret_value")) {
		t.Fatalf("raw = %q, %v", raw, err)
	}
}

func TestPutValidates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	for name, ss := range map[string]config.ScaleSet{
		"bad url":            {Name: "x", URL: "https://gitlab.com/o", Credential: "file-cred"},
		"unknown credential": {Name: "x", URL: "https://github.com/o", Credential: "nope"},
		"bad name":           {Name: "Bad Name", URL: "https://github.com/o", Credential: "file-cred"},
		"tiny memory":        {Name: "x", URL: "https://github.com/o", Credential: "file-cred", MemoryMB: 64},
	} {
		if err := r.PutScaleSet(ctx, ss); err == nil || errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: err = %v, want a validation error", name, err)
		}
	}
	if err := r.PutCredential(ctx, "Bad Name", "x"); err == nil {
		t.Error("credential names follow the scale set name rules")
	}
	if err := r.PutCredential(ctx, "ok", "  "); err == nil {
		t.Error("an empty token must be refused")
	}
}

func TestSubscribersHearChanges(t *testing.T) {
	e := newEnv(t)
	r := e.registry(t)
	ch, cancel := r.Subscribe()
	defer cancel()
	_ = r.PutCredential(context.Background(), "ui-cred", "github_pat_ui")
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no notification")
	}
}

func TestCreatingAScaleSetNeverReplacesOne(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	if err := r.PutCredential(ctx, "ui-cred", "github_pat_ui"); err != nil {
		t.Fatal(err)
	}
	ss := config.ScaleSet{Name: "ui-ss", URL: "https://github.com/o", Credential: "ui-cred", MaxConcurrent: 3}
	if err := r.CreateScaleSet(ctx, ss); err != nil {
		t.Fatal(err)
	}
	ss.MaxConcurrent = 9
	if err := r.CreateScaleSet(ctx, ss); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if got, _ := r.ScaleSet("ui-ss"); got.MaxConcurrent != 3 {
		t.Fatalf("replaced: %+v", got)
	}
	ss.Name = "file-ss"
	if err := r.CreateScaleSet(ctx, ss); !errors.Is(err, ErrExists) {
		t.Fatalf("a file scale set's name: err = %v, want ErrExists", err)
	}
}

func TestCapacityIsEditedWithoutAFileSection(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	if c, src := r.Capacity(); src != CapacityDefault || c != e.cfg.Capacity {
		t.Fatalf("Capacity() = %+v, %q; want the defaults", c, src)
	}
	changes, stop := r.Subscribe()
	defer stop()
	want := config.Capacity{MaxEnvironments: 6, MemoryBudgetMB: 49152, MemoryMarginMB: 2048, MaxDiskPercent: 90}
	if err := r.PutCapacity(ctx, want); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changes:
	default:
		t.Error("a capacity change is announced")
	}
	if c, src := r.Capacity(); src != SourceUI || c != want {
		t.Fatalf("Capacity() = %+v, %q; want %+v from the UI", c, src, want)
	}
	// It survives a restart.
	if c, src := e.registry(t).Capacity(); src != SourceUI || c != want {
		t.Fatalf("after a restart Capacity() = %+v, %q", c, src)
	}
}

func TestCapacityRejectsInvalidLimits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.cfg.Capacity = config.Capacity{MaxEnvironments: 4, MemoryBudgetMB: 16384, MemoryMarginMB: 4096, MaxDiskPercent: 85}
	r := e.registry(t)
	if err := r.PutCapacity(ctx, config.Capacity{MaxEnvironments: 0, MemoryBudgetMB: 16384, MaxDiskPercent: 85}); err == nil {
		t.Error("no environments allowed must be rejected")
	}
	// file-ss runs 4096 MB environments: a smaller budget would never start one.
	err := r.PutCapacity(ctx, config.Capacity{MaxEnvironments: 4, MemoryBudgetMB: 2048, MaxDiskPercent: 85})
	if err == nil || !strings.Contains(err.Error(), "file-ss") {
		t.Errorf("PutCapacity below a scale set's memory = %v, want an error naming it", err)
	}
	if _, src := r.Capacity(); src != CapacityDefault {
		t.Error("a rejected change is not kept")
	}
}

func TestCapacityInTheFileIsReadOnly(t *testing.T) {
	e := newEnv(t)
	e.cfg.Capacity = config.Capacity{MaxEnvironments: 2, MemoryBudgetMB: 8192, MemoryMarginMB: 4096, MaxDiskPercent: 85}
	e.cfg.CapacityInFile = true
	r := e.registry(t)
	err := r.PutCapacity(context.Background(), config.Capacity{MaxEnvironments: 6, MemoryBudgetMB: 16384, MaxDiskPercent: 85})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("PutCapacity = %v, want ErrReadOnly", err)
	}
	if c, src := r.Capacity(); src != SourceFile || c.MaxEnvironments != 2 {
		t.Fatalf("Capacity() = %+v, %q", c, src)
	}
}

func TestScaleSetMemoryMustFitTheBudget(t *testing.T) {
	e := newEnv(t)
	e.cfg.Capacity = config.Capacity{MaxEnvironments: 4, MemoryBudgetMB: 8192, MemoryMarginMB: 4096, MaxDiskPercent: 85}
	r := e.registry(t)
	err := r.PutScaleSet(context.Background(), config.ScaleSet{Name: "big", URL: "https://github.com/o/r", Credential: "file-cred", MemoryMB: 12288})
	if err == nil || !strings.Contains(err.Error(), "memory budget") {
		t.Fatalf("PutScaleSet above the budget = %v, want an error", err)
	}
}

func TestStoredCapacityIsNormalized(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// An older or hand-edited row: missing fields take the defaults.
	if err := e.db.PutMeta(ctx, "capacity", `{"memory_budget_mb": 49152}`); err != nil {
		t.Fatal(err)
	}
	r := e.registry(t)
	if c, src := r.Capacity(); src != SourceUI || c.MemoryBudgetMB != 49152 || c.MaxEnvironments != 4 || c.MaxDiskPercent != 85 {
		t.Fatalf("Capacity() = %+v, %q", c, src)
	}
	if w := r.Warnings(); len(w) != 0 {
		t.Fatalf("Warnings() = %v", w)
	}
	// A row that cannot work falls back to the defaults, with a warning.
	if err := e.db.PutMeta(ctx, "capacity", `{"max_environments": 500, "memory_budget_mb": 8192}`); err != nil {
		t.Fatal(err)
	}
	r = e.registry(t)
	if _, src := r.Capacity(); src != CapacityDefault {
		t.Fatalf("an invalid row is used: %q", src)
	}
	if w := r.Warnings(); len(w) != 1 || !strings.Contains(w[0], "ignored") {
		t.Fatalf("Warnings() = %v", w)
	}
}

func TestAScaleSetAboveALoweredBudgetStaysEditable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	r := e.registry(t)
	big := config.ScaleSet{Name: "big", URL: "https://github.com/o/r", Credential: "file-cred", MemoryMB: 12288}
	if err := r.PutScaleSet(ctx, big); err != nil {
		t.Fatal(err)
	}
	// The file now sets a smaller budget.
	e.cfg.Capacity.MemoryBudgetMB, e.cfg.CapacityInFile = 8192, true
	r = e.registry(t)
	if w := r.Warnings(); len(w) != 1 || !strings.Contains(w[0], "big") {
		t.Fatalf("Warnings() = %v, want one naming big", w)
	}
	big.Labels = []string{"gpu"}
	if err := r.PutScaleSet(ctx, big); err != nil {
		t.Fatalf("a change that keeps the memory = %v", err)
	}
	big.MemoryMB = 16384
	if err := r.PutScaleSet(ctx, big); err == nil {
		t.Fatal("raising the memory further above the budget must be refused")
	}
}
