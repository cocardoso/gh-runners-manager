package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/cachemon"
	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func TestCredentialEndpointsNeverReturnTheToken(t *testing.T) {
	h := newHarness(t, "")
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	if resp, b := h.call(t, "PUT", "/api/v1/credentials/home", map[string]string{"token": "github_pat_good"}, tok); resp.StatusCode != 204 {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	resp, b := h.call(t, "GET", "/api/v1/credentials", nil, tok)
	body := string(b)
	if resp.StatusCode != 200 || strings.Contains(body, "github_pat_good") || !strings.Contains(body, `"name":"home"`) ||
		!strings.Contains(body, `"source":"ui"`) || !strings.Contains(body, `"token_hint":"good"`) || !strings.Contains(body, `"name":"c"`) {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	_, b = h.call(t, "POST", "/api/v1/credentials/home/test", nil, tok)
	if !strings.Contains(string(b), `"ok":true`) || !strings.Contains(string(b), "octocat") {
		t.Fatalf("test = %s", b)
	}
	_ = h.reg.PutCredential(context.Background(), "bad", "github_pat_bad")
	if _, b = h.call(t, "POST", "/api/v1/credentials/bad/test", nil, tok); !strings.Contains(string(b), `"ok":false`) || !strings.Contains(string(b), "Bad credentials") {
		t.Fatalf("bad test = %s", b)
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/credentials/c", map[string]string{"token": "x"}, tok); resp.StatusCode != 409 {
		t.Fatalf("file credential put = %d, want 409", resp.StatusCode)
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/credentials/Bad%20Name", map[string]string{"token": "x"}, tok); resp.StatusCode != 422 {
		t.Fatalf("bad name = %d, want 422", resp.StatusCode)
	}
	_ = h.reg.PutScaleSet(context.Background(), config.ScaleSet{Name: "ui-ss", URL: "https://github.com/o", Credential: "home"})
	if resp, _ := h.call(t, "DELETE", "/api/v1/credentials/home", nil, tok); resp.StatusCode != 409 {
		t.Fatalf("delete in use = %d, want 409", resp.StatusCode)
	}
	if resp, _ := h.call(t, "DELETE", "/api/v1/credentials/bad", nil, tok); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp, _ := h.call(t, "DELETE", "/api/v1/credentials/bad", nil, tok); resp.StatusCode != 404 {
		t.Fatalf("delete again = %d, want 404", resp.StatusCode)
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Kind]++
	}
	if kinds["audit.credential_put"] != 1 || kinds["audit.credential_delete"] != 1 {
		t.Fatalf("audit = %v", kinds)
	}
}

func TestScaleSetEndpoints(t *testing.T) {
	h := newHarness(t, "")
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	body := map[string]any{"url": "https://github.com/o/new", "credential": "c", "labels": []string{"big"}, "memory_mb": 2048, "warm_runners": 1}
	if resp, b := h.call(t, "PUT", "/api/v1/scale-sets/new-set", body, tok); resp.StatusCode != 204 {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	ss, ok := h.reg.ScaleSet("new-set")
	if !ok || ss.MemoryMB != 2048 || ss.Cores != 2 || ss.Labels[0] != "big" || ss.WarmRunners != 1 {
		t.Fatalf("stored = %+v %v", ss, ok)
	}
	for name, bad := range map[string]map[string]any{
		"bad url":             {"url": "https://example.com/o", "credential": "c"},
		"unknown credential":  {"url": "https://github.com/o", "credential": "nope"},
		"warm over the limit": {"url": "https://github.com/o", "credential": "c", "max_concurrent": 1, "warm_runners": 2},
	} {
		if resp, _ := h.call(t, "PUT", "/api/v1/scale-sets/x", bad, tok); resp.StatusCode != 422 {
			t.Errorf("%s = %d, want 422", name, resp.StatusCode)
		}
	}
	// Creating (If-None-Match: *) never replaces a scale set of the same name.
	create := map[string]string{"Authorization": "Bearer " + harnessToken, "If-None-Match": "*"}
	if code, b := h.callCode(t, "PUT", "/api/v1/scale-sets/new-set", body, create); code != 412 || !strings.Contains(string(b), "already exists") {
		t.Fatalf("create over new-set = %d %s, want 412", code, b)
	}
	if code, b := h.callCode(t, "PUT", "/api/v1/scale-sets/other-set", body, create); code != 204 {
		t.Fatalf("create = %d %s", code, b)
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/scale-sets/lab", body, tok); resp.StatusCode != 409 {
		t.Fatalf("file scale set = %d, want 409", resp.StatusCode)
	}
	_, b := h.call(t, "GET", "/api/v1/scale-sets", nil, tok)
	if !strings.Contains(string(b), `"source":"file"`) || !strings.Contains(string(b), `"url":"https://github.com/o/r"`) {
		t.Fatalf("list = %s", b)
	}
	if resp, _ := h.call(t, "DELETE", "/api/v1/scale-sets/new-set", nil, tok); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if _, ok := h.reg.ScaleSet("new-set"); ok {
		t.Fatal("still there")
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Kind]++
	}
	if kinds["audit.scale_set_put"] != 2 || kinds["audit.scale_set_delete"] != 1 {
		t.Fatalf("audit = %v", kinds)
	}
}

type fakeCache struct{ s cachemon.Status }

func (f fakeCache) Status() cachemon.Status { return f.s }

func TestCacheEndpointAndAlert(t *testing.T) {
	since := time.Date(2026, 10, 8, 1, 36, 22, 0, time.UTC)
	down := cachemon.Status{Enabled: true, Address: "10.50.0.3", Up: false, CheckedAt: time.Now(), DownSince: since,
		Origins: []cachemon.OriginStatus{{Origin: "docker.io", Error: "connection refused"}}}
	h := newHarnessWith(t, "", func(d *Deps) { d.Cache = fakeCache{down} })
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	_, b := h.call(t, "GET", "/api/v1/cache", nil, tok)
	if !strings.Contains(string(b), `"enabled":true`) || !strings.Contains(string(b), `"origin":"docker.io"`) || !strings.Contains(string(b), "connection refused") {
		t.Fatalf("cache = %s", b)
	}
	_, b = h.call(t, "GET", "/api/v1/overview", nil, tok)
	if !strings.Contains(string(b), `"kind":"cache_down"`) || !strings.Contains(string(b), `"time":"2026-10-08T01:36:22Z"`) {
		t.Fatalf("overview lacks the cache alert, dated when the cache went down: %s", b)
	}
	none := newHarness(t, "")
	_, b = none.call(t, "GET", "/api/v1/cache", nil, tok)
	if !strings.Contains(string(b), `"enabled":false`) {
		t.Fatalf("no cache = %s", b)
	}
}

func TestCapacityEndpoints(t *testing.T) {
	h := newHarnessWith(t, "", func(d *Deps) {
		d.Config.Capacity.ApplyDefaults()
		d.Config.ScaleSets[0].ApplyDefaults()
		d.Capacity = func(context.Context) (runtime.Capacity, error) {
			return runtime.Capacity{HostMemoryTotalMB: 40960, HostMemoryAvailableMB: 30000}, nil
		}
	})
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.callCode(t, "GET", "/api/v1/capacity", nil, tok)
	if code != 200 || !strings.Contains(string(b), `"source":"default"`) || !strings.Contains(string(b), `"memory_budget_mb":16384`) ||
		!strings.Contains(string(b), `"host_memory_total_mb":40960`) {
		t.Fatalf("get = %d %s", code, b)
	}
	if code, _ := h.callCode(t, "PUT", "/api/v1/capacity", map[string]any{"max_environments": 6, "memory_budget_mb": 49152, "memory_margin_mb": 2048, "max_disk_percent": 90},
		map[string]string{"X-Test-No-Auth": "1"}); code != 401 {
		t.Fatalf("put without credentials = %d, want 401", code)
	}
	if code, b := h.callCode(t, "PUT", "/api/v1/capacity", map[string]any{"max_environments": 6, "memory_budget_mb": 49152, "memory_margin_mb": 2048, "max_disk_percent": 90}, tok); code != 204 {
		t.Fatalf("put = %d %s", code, b)
	}
	code, b = h.callCode(t, "GET", "/api/v1/capacity", nil, tok)
	if code != 200 || !strings.Contains(string(b), `"source":"ui"`) || !strings.Contains(string(b), `"memory_budget_mb":49152`) {
		t.Fatalf("get after put = %d %s", code, b)
	}
	// The overview and the settings show the limits in effect.
	if _, b := h.callCode(t, "GET", "/api/v1/overview", nil, tok); !strings.Contains(string(b), `"memory_budget_mb":49152`) || !strings.Contains(string(b), `"environments_max":6`) {
		t.Fatalf("overview = %s", b)
	}
	if _, b := h.callCode(t, "GET", "/api/v1/settings", nil, tok); !strings.Contains(string(b), `"memory_budget_mb":49152`) {
		t.Fatalf("settings = %s", b)
	}
	// lab runs 4096 MB environments.
	if code, b := h.callCode(t, "PUT", "/api/v1/capacity", map[string]any{"max_environments": 6, "memory_budget_mb": 2048, "memory_margin_mb": 0, "max_disk_percent": 90}, tok); code != 422 || !strings.Contains(string(b), "lab") {
		t.Fatalf("budget below a scale set = %d %s, want 422", code, b)
	}
	if code, _ := h.callCode(t, "PUT", "/api/v1/capacity", map[string]any{"max_environments": 0, "memory_budget_mb": 8192, "memory_margin_mb": 0, "max_disk_percent": 90}, tok); code != 422 {
		t.Fatalf("no environments = %d, want 422", code)
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	found := false
	for _, e := range evs {
		found = found || e.Kind == "audit.capacity_put"
	}
	if !found {
		t.Error("a capacity change is audited")
	}
}

func TestCapacityInTheFileIsReadOnlyOverTheAPI(t *testing.T) {
	h := newHarnessWith(t, "", func(d *Deps) {
		d.Config.Capacity = config.Capacity{MaxEnvironments: 2, MemoryBudgetMB: 8192, MemoryMarginMB: 1024, MaxDiskPercent: 85}
		d.Config.CapacityInFile = true
	})
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	if _, b := h.callCode(t, "GET", "/api/v1/capacity", nil, tok); !strings.Contains(string(b), `"source":"file"`) {
		t.Fatalf("get = %s", b)
	}
	if code, _ := h.callCode(t, "PUT", "/api/v1/capacity", map[string]any{"max_environments": 6, "memory_budget_mb": 16384, "memory_margin_mb": 0, "max_disk_percent": 90}, tok); code != 409 {
		t.Fatalf("put = %d, want 409", code)
	}
}
