package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repo creates a cached repository of size bytes whose files were last used at t.
func repo(t *testing.T, root, origin, name string, size int, used time.Time) {
	t.Helper()
	dir := filepath.Join(root, origin, "docker/registry/v2/repositories", name)
	for _, sub := range []string{"_manifests/tags/latest/current", "_layers/sha256/aa"} {
		_ = os.MkdirAll(filepath.Join(dir, sub), 0o755)
	}
	link := filepath.Join(dir, "_manifests/tags/latest/current/link")
	_ = os.WriteFile(link, []byte("sha256:x"), 0o644)
	blob := filepath.Join(root, origin, "docker/registry/v2/blobs/sha256", name+"-data")
	_ = os.MkdirAll(filepath.Dir(blob), 0o755)
	_ = os.WriteFile(blob, make([]byte, size), 0o644)
	_ = os.Chtimes(link, used, used)
}

type pruneHarness struct {
	root  string
	cmds  []string
	prune CachePrune
}

func newPrune(t *testing.T, budget int64) *pruneHarness {
	h := &pruneHarness{root: t.TempDir()}
	h.prune = CachePrune{Root: h.root, BudgetBytes: budget, HighPercent: 85, LowPercent: 70,
		Instances:  map[string]string{"docker.io": "/etc/ghrm-cache/docker.io.yml", "ghcr.io": "/etc/ghrm-cache/ghcr.io.yml"},
		StatusPath: filepath.Join(h.root, "status"),
		Run: func(_ context.Context, name string, args ...string) error {
			h.cmds = append(h.cmds, name+" "+strings.Join(args, " "))
			// Garbage collection frees the blobs of deleted repositories.
			if name == "registry" {
				_ = os.RemoveAll(filepath.Join(h.root, "docker.io/docker/registry/v2/blobs/sha256/old-data"))
			}
			return nil
		}}
	return h
}

func TestPruneDoesNothingUnderTheHighMark(t *testing.T) {
	h := newPrune(t, 1000)
	repo(t, h.root, "docker.io", "small", 100, time.Now())
	if err := h.prune.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.cmds) != 0 {
		t.Fatalf("commands = %v; under the budget nothing is evicted", h.cmds)
	}
	if b, _ := os.ReadFile(h.prune.StatusPath); !strings.Contains(string(b), "budget_bytes 1000") || !strings.Contains(string(b), "used_bytes ") {
		t.Fatalf("status = %q", b)
	}
}

func TestPruneEvictsLeastRecentlyUsedFirstAndCollectsGarbage(t *testing.T) {
	h := newPrune(t, 1000)
	now := time.Now()
	repo(t, h.root, "docker.io", "old", 600, now.Add(-72*time.Hour))
	repo(t, h.root, "ghcr.io", "recent", 300, now)
	if err := h.prune.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.root, "docker.io/docker/registry/v2/repositories/old")); !os.IsNotExist(err) {
		t.Fatal("the least recently used repository must be evicted")
	}
	if _, err := os.Stat(filepath.Join(h.root, "ghcr.io/docker/registry/v2/repositories/recent")); err != nil {
		t.Fatal("the recent repository must stay (usage is now below the low mark)")
	}
	want := []string{"systemctl stop ghrm-cache-registry@docker.io", "registry garbage-collect --delete-untagged /etc/ghrm-cache/docker.io.yml", "systemctl start ghrm-cache-registry@docker.io"}
	if strings.Join(h.cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %v, want %v (only the touched instance)", h.cmds, want)
	}
}

func TestCacheExporterServesTheStatus(t *testing.T) {
	dir := t.TempDir()
	status := filepath.Join(dir, "status")
	srv := httptest.NewServer(CacheExporter(status))
	defer srv.Close()
	if resp, _ := http.Get(srv.URL + "/metrics"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("without a status = %d, want 503", resp.StatusCode)
	}
	_ = os.WriteFile(status, []byte("used_bytes 123\nbudget_bytes 1000\n"), 0o644)
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := make([]byte, 1024)
	n, _ := resp.Body.Read(b)
	body := string(b[:n])
	for _, want := range []string{"ghrm_cache_disk_used_bytes 123", "ghrm_cache_disk_budget_bytes 1000"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
}
