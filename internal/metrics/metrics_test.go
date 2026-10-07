package metrics

import (
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type fakeScaleSets []controller.ScaleSetStatus

func (f fakeScaleSets) ScaleSets(context.Context) []controller.ScaleSetStatus { return f }

func TestMetricsExposeTheFleet(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, st := range []string{"running", "running", "booting", "destroyed"} {
		_ = db.CreateEnvironment(ctx, store.Environment{ID: string(rune('a' + i)), ScaleSet: "lab", State: st, MemoryMB: 512})
	}
	_ = db.CreateEnvironment(ctx, store.Environment{ID: "f", ScaleSet: "lab", State: "failed", FailureStage: "timeout:booting", MemoryMB: 512})
	_ = db.UpsertJob(ctx, store.Job{ID: "j1", ScaleSet: "lab", Status: "completed", Result: "succeeded"})
	_ = db.UpsertJob(ctx, store.Job{ID: "j2", ScaleSet: "lab", Status: "completed", Result: "failed"})
	_ = db.CreateTemplate(ctx, store.Template{ID: "t1", State: store.TemplateFailed, SlimRelease: "1", RunnerVersion: "1", LayerVersion: "1"})

	m := New(db, fakeScaleSets{{Name: "lab", Desired: 3, Listening: true}}, "v1.2.3")
	m.ObserveStage("booting", 4*time.Second)
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	for _, want := range []string{
		`ghrm_environments{scale_set="lab",state="running"} 2`,
		`ghrm_environments{scale_set="lab",state="booting"} 1`,
		`ghrm_environment_failures_total{stage="timeout:booting"} 1`,
		`ghrm_jobs_total{result="succeeded",scale_set="lab"} 1`,
		`ghrm_jobs_total{result="failed",scale_set="lab"} 1`,
		`ghrm_scale_set_desired{scale_set="lab"} 3`,
		`ghrm_scale_set_listening{scale_set="lab"} 1`,
		`ghrm_template_builds_total{result="failed"} 1`,
		`ghrm_stage_duration_seconds_count{stage="booting"} 1`,
		`ghrm_build_info{version="v1.2.3"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, `state="destroyed"`) {
		t.Error("destroyed environments are history, not fleet")
	}
}
