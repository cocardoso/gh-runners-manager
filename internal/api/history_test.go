package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/retention"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func (h *harness) callCode(t *testing.T, method, path string, body any, hdr map[string]string) (int, []byte) {
	t.Helper()
	resp, b := h.call(t, method, path, body, hdr)
	return resp.StatusCode, b
}

func TestHistoryEndpointsNeedSignIn(t *testing.T) {
	h := historyHarness(t)
	for _, r := range []struct{ method, path string }{{"PUT", "/api/v1/history/settings"}, {"POST", "/api/v1/history/cleanup"}, {"DELETE", "/api/v1/templates/x"}, {"DELETE", "/api/v1/environments/x"}} {
		if code, _ := h.callCode(t, r.method, r.path, map[string]any{}, map[string]string{"X-Test-No-Auth": "1"}); code != 401 {
			t.Errorf("%s %s without credentials = %d, want 401", r.method, r.path, code)
		}
	}
}

func historyHarness(t *testing.T) *harness {
	return newHarnessWith(t, "", func(d *Deps) {
		d.History = &retention.Retention{Store: d.Store, Logs: d.Logs, Recorder: d.Recorder}
	})
}

func TestHistorySettingsRoundTrip(t *testing.T) {
	h := historyHarness(t)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	code, b := h.callCode(t, "GET", "/api/v1/history/settings", nil, tok)
	if code != 200 || !strings.Contains(string(b), `"mode":"automatic"`) || !strings.Contains(string(b), `"days":30`) {
		t.Fatalf("%d %s", code, b)
	}
	code, b = h.callCode(t, "PUT", "/api/v1/history/settings", map[string]any{"mode": "manual", "days": 7, "audit_days": 3}, tok)
	if code != 422 && code != 400 {
		t.Fatalf("audit < days accepted: %d %s", code, b)
	}
	code, _ = h.callCode(t, "PUT", "/api/v1/history/settings", map[string]any{"mode": "manual", "days": 7, "audit_days": 90}, tok)
	if code != 204 {
		t.Fatalf("put = %d", code)
	}
	if _, b = h.callCode(t, "GET", "/api/v1/history/settings", nil, tok); !strings.Contains(string(b), `"mode":"manual"`) {
		t.Fatalf("%s", b)
	}
}

func TestHistoryCleanupDryRunAndRun(t *testing.T) {
	h := historyHarness(t)
	ctx := context.Background()
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	old := time.Now().Add(-40 * 24 * time.Hour)
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "e1", ScaleSet: "ss", State: "destroyed", CreatedAt: old, UpdatedAt: old, StateChangedAt: old})
	before := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	code, b := h.callCode(t, "POST", "/api/v1/history/cleanup", map[string]any{"before": before, "dry_run": true}, tok)
	if code != 200 || !strings.Contains(string(b), `"environments":1`) {
		t.Fatalf("dry run: %d %s", code, b)
	}
	if _, err := h.db.GetEnvironment(ctx, "e1"); err != nil {
		t.Fatal("dry run deleted")
	}
	if code, b = h.callCode(t, "POST", "/api/v1/history/cleanup", map[string]any{"before": before}, tok); code != 200 || !strings.Contains(string(b), `"environments":1`) {
		t.Fatalf("run: %d %s", code, b)
	}
	if _, err := h.db.GetEnvironment(ctx, "e1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("not deleted")
	}
	if !hasEvent(t, h.db, "audit.history_cleanup") {
		t.Fatal("cleanup not audited")
	}
}

func TestHistoryCleanupRejectsTheFuture(t *testing.T) {
	h := historyHarness(t)
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if code, b := h.callCode(t, "POST", "/api/v1/history/cleanup", map[string]any{"before": future}, tok); code != 422 && code != 400 {
		t.Fatalf("future cut-off accepted: %d %s", code, b)
	}
}

func TestDeleteTemplateAndEnvironmentRecords(t *testing.T) {
	h := historyHarness(t)
	ctx := context.Background()
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	now := time.Now()
	_ = h.db.CreateTemplate(ctx, store.Template{ID: "tf", State: store.TemplateFailed, CreatedAt: now, UpdatedAt: now})
	_ = h.db.CreateTemplate(ctx, store.Template{ID: "ta", State: store.TemplateReady, CreatedAt: now, UpdatedAt: now})
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "ed", ScaleSet: "ss", State: "destroyed", CreatedAt: now, UpdatedAt: now, StateChangedAt: now})
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "el", ScaleSet: "ss", State: "idle", CreatedAt: now, UpdatedAt: now, StateChangedAt: now})
	for path, want := range map[string]int{
		"/api/v1/templates/tf": 204, "/api/v1/templates/ta": 409, "/api/v1/templates/nope": 404,
		"/api/v1/environments/ed": 204, "/api/v1/environments/el": 409, "/api/v1/environments/nope": 404,
	} {
		if code, b := h.callCode(t, "DELETE", path, nil, tok); code != want {
			t.Errorf("DELETE %s = %d %s, want %d", path, code, b, want)
		}
	}
	if !hasEvent(t, h.db, "audit.template_delete") || !hasEvent(t, h.db, "audit.environment_delete") {
		t.Fatal("deletes not audited")
	}
}

func hasEvent(t *testing.T, db *store.Store, kind string) bool {
	t.Helper()
	evs, _ := db.ListEvents(context.Background(), store.EventFilter{})
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
