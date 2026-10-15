package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/controller"
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
		FailureStage: stage, FailureReason: reason, FailedAt: at, CreatedAt: at, UpdatedAt: at}); err != nil {
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
	code, body := h.callCode(t, http.MethodPost, "/api/v1/alerts/dismiss", dismissBody(as[0]), h.bearer())
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

func TestADestroyAfterTheDismissalDoesNotBringTheAlertBack(t *testing.T) {
	h := newHarness(t, "")
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	if err := h.db.CreateEnvironment(ctx, store.Environment{ID: "e1", ScaleSet: "farma-bot", State: "failed",
		FailureStage: "start", FailureReason: "boom", FailedAt: at, CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	as := h.alerts(t)
	if code, body := h.callCode(t, http.MethodPost, "/api/v1/alerts/dismiss", dismissBody(as[0]), h.bearer()); code != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s", code, body)
	}
	// The failed environment is destroyed after the dismissal: no new failure.
	for _, to := range []string{"destroying", "destroyed"} {
		if _, err := h.db.TransitionEnvironment(ctx, "e1", nil, to, nil); err != nil {
			t.Fatal(err)
		}
	}
	if as := h.alerts(t); len(as) != 0 {
		t.Fatalf("alerts after the destroy = %+v, want none", as)
	}
}

func TestDismissRejectsUnknownBodies(t *testing.T) {
	h := newHarness(t, "")
	if code, _ := h.callCode(t, http.MethodPost, "/api/v1/alerts/dismiss", map[string]any{"alerts": []any{}}, h.bearer()); code != http.StatusUnprocessableEntity {
		t.Fatalf("dismiss with no ids = %d, want 422", code)
	}
}

func (h *harness) bearer() map[string]string {
	return map[string]string{"Authorization": "Bearer " + h.token}
}

func dismissBody(as ...alertView) map[string]any {
	list := make([]map[string]any, len(as))
	for i, a := range as {
		list[i] = map[string]any{"id": a.ID, "time": a.Time}
	}
	return map[string]any{"alerts": list}
}

func TestFailuresSeenAfterTheOperatorLookedStayVisible(t *testing.T) {
	h := newHarness(t, "")
	now := time.Now()
	failed(t, h.db, "e1", "farma-bot", "start", startFailure(900, 1), now.Add(-2*time.Minute))
	seen := h.alerts(t)[0]
	// Another failure happens while the operator reads the page, before the click.
	failed(t, h.db, "e2", "farma-bot", "start", startFailure(901, 2), now.Add(-time.Minute))
	if code, body := h.callCode(t, http.MethodPost, "/api/v1/alerts/dismiss", dismissBody(seen), h.bearer()); code != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s", code, body)
	}
	as := h.alerts(t)
	if len(as) != 1 || as[0].Count != 1 || as[0].EnvironmentID != "e2" {
		t.Fatalf("alerts = %+v, want the failure the operator had not seen", as)
	}
}

func TestGroupingIgnoresGuestAndRunnerIDsButKeepsCodes(t *testing.T) {
	h := newHarness(t, "")
	now := time.Now()
	reasons := []string{
		"command 'lxc-start' failed: exit code 1",
		"command 'lxc-start' failed: exit code 255",
		"github: runner ghrm-01m50bexzjg2 already exists (status 409)",
		"github: runner ghrm-01m50fq8x7yt already exists (status 409)",
		"agent at 10.50.0.101:8443 did not answer within 3m0s",
		"agent at 10.50.0.117:8443 did not answer within 3m0s",
		"destroy vm-901-disk-0 on local-lvm: volume in use",
		"destroy vm-902-disk-0 on local-lvm: volume in use",
	}
	for i, r := range reasons {
		failed(t, h.db, fmt.Sprintf("e%d", i), "lab", "boot", r, now.Add(-time.Duration(len(reasons)-i)*time.Second))
	}
	counts := map[int]int{}
	for _, a := range h.alerts(t) {
		counts[a.Count]++
	}
	if counts[1] != 2 || counts[2] != 3 {
		t.Fatalf("group sizes = %v, want two single exit codes and three pairs", counts)
	}
}

func TestADismissedWaitingAlertStaysDismissedUntilANewWait(t *testing.T) {
	h := newHarness(t, "")
	// The controller's clock has nanoseconds; the store keeps milliseconds.
	since := time.Date(2026, 10, 15, 12, 0, 0, 123456789, time.UTC)
	h.ctl.sets = []controller.ScaleSetStatus{{Name: "lab", Desired: 2, Live: 1, Waiting: "provision_failing", WaitingSince: since}}
	as := h.alerts(t)
	if len(as) != 1 || as[0].Kind != "waiting" {
		t.Fatalf("alerts = %+v, want the waiting alert", as)
	}
	if code, body := h.callCode(t, http.MethodPost, "/api/v1/alerts/dismiss", dismissBody(as[0]), h.bearer()); code != http.StatusNoContent {
		t.Fatalf("dismiss = %d %s", code, body)
	}
	if as := h.alerts(t); len(as) != 0 {
		t.Fatalf("alerts after dismiss = %+v, want none", as)
	}
	h.ctl.sets[0].WaitingSince = since.Add(time.Minute)
	if as := h.alerts(t); len(as) != 1 {
		t.Fatalf("alerts after a new wait = %+v, want it back", as)
	}
}
