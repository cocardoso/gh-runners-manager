package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type alertView struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Message       string    `json:"message"`
	ScaleSet      string    `json:"scale_set"`
	EnvironmentID string    `json:"environment_id"`
	Count         int       `json:"count"`
	FirstTime     time.Time `json:"first_time"`
	Time          time.Time `json:"time"`
	Dismissible   bool      `json:"dismissible"`
}

func (h *harness) alerts(t *testing.T) []alertView {
	t.Helper()
	var ov struct{ Alerts []alertView }
	if code := h.getJSON(t, "/api/v1/overview", &ov); code != 200 {
		t.Fatalf("overview = %d", code)
	}
	return ov.Alerts
}

func failed(t *testing.T, db *store.Store, id, scaleSet, stage, reason string, at time.Time) {
	t.Helper()
	if err := db.CreateEnvironment(context.Background(), store.Environment{ID: id, ScaleSet: scaleSet, State: "destroyed",
		FailureStage: stage, FailureReason: reason, CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func startFailure(vmid, upid int) string {
	return fmt.Sprintf("proxmox task UPID:pve:%08X:0485:6AD1:vzstart:%d:ghrm@pve!ghrm: failed: unable to get PID for CT %d (not running?)", upid, vmid, vmid)
}

func TestFailuresAreGroupedByScaleSetStageAndReason(t *testing.T) {
	h := newHarness(t, "")
	now := time.Now()
	for i := range 30 {
		failed(t, h.db, fmt.Sprintf("e%02d", i), "farma-bot", "start", startFailure(900+i%2, i), now.Add(-time.Duration(30-i)*time.Second))
	}
	failed(t, h.db, "other-set", "lab", "start", startFailure(900, 99), now.Add(-time.Minute))
	failed(t, h.db, "other-stage", "farma-bot", "create", "boom", now.Add(-time.Minute))
	failed(t, h.db, "old", "farma-bot", "start", startFailure(900, 98), now.Add(-25*time.Hour))

	var groups []alertView
	for _, a := range h.alerts(t) {
		if a.Kind == "environment_failed" {
			groups = append(groups, a)
		}
	}
	if len(groups) != 3 {
		t.Fatalf("groups = %+v, want 3 (farma-bot/start, lab/start, farma-bot/create)", groups)
	}
	g := groups[0]
	if g.ScaleSet != "farma-bot" || g.Count != 30 || g.EnvironmentID != "e29" || !g.Dismissible || g.ID == "" {
		t.Fatalf("first group = %+v, want the 30 recent farma-bot start failures, latest e29, dismissible", g)
	}
	if !g.FirstTime.Before(g.Time) || g.Message != "environment failed at start: "+startFailure(901, 29) {
		t.Fatalf("group times %v..%v, message %q", g.FirstTime, g.Time, g.Message)
	}
}

func TestDismissedAlertsStayHiddenUntilTheyHappenAgain(t *testing.T) {
	h := newHarness(t, "")
	now := time.Now()
	failed(t, h.db, "e1", "farma-bot", "start", startFailure(900, 1), now.Add(-2*time.Minute))
	failed(t, h.db, "e2", "farma-bot", "start", startFailure(901, 2), now.Add(-time.Minute))
	as := h.alerts(t)
	if len(as) != 1 {
		t.Fatalf("alerts = %+v, want 1 group", as)
	}
	id := as[0].ID
	code, body := h.callCode(t, http.MethodPost, "/api/v1/alerts/dismiss", map[string]any{"ids": []string{as[0].ID}}, h.bearer())
	if code != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s", code, body)
	}
	if as := h.alerts(t); len(as) != 0 {
		t.Fatalf("alerts after dismiss = %+v, want none", as)
	}
	// A new failure of the same kind brings the group back, counting only what is new.
	failed(t, h.db, "e3", "farma-bot", "start", startFailure(900, 3), time.Now().Add(time.Second))
	as = h.alerts(t)
	if len(as) != 1 || as[0].Count != 1 || as[0].EnvironmentID != "e3" || as[0].ID != id {
		t.Fatalf("alerts after a new failure = %+v, want the group again with 1 occurrence", as)
	}
}

func TestDismissRejectsUnknownBodies(t *testing.T) {
	h := newHarness(t, "")
	if code, _ := h.callCode(t, http.MethodPost, "/api/v1/alerts/dismiss", map[string]any{"ids": []string{}}, h.bearer()); code != http.StatusUnprocessableEntity {
		t.Fatalf("dismiss with no ids = %d, want 422", code)
	}
}

func (h *harness) bearer() map[string]string {
	return map[string]string{"Authorization": "Bearer " + h.token}
}
