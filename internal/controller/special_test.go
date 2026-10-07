package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

type fakeSource struct{}

func (fakeSource) Active(context.Context) (string, int) { return "tpl/abc", 951 }

type fakeTemplateEvents struct {
	mu  sync.Mutex
	got []string
}

func (f *fakeTemplateEvents) AgentEvent(_ context.Context, envID, name string, _ time.Time, _ map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, envID+":"+name)
}

func TestJobEnvironmentsCloneTheActiveTemplate(t *testing.T) {
	h := newHarness(t, nil)
	h.c.d.Templates = fakeSource{}
	envs := h.provision(t, 1)
	if len(envs) != 1 || envs[0].TemplateVMID != 951 || envs[0].Kind != store.KindJob {
		t.Fatalf("env = %+v", envs)
	}
	spec, _ := h.rt.Spec(refOf(envs[0]))
	if spec.Template != "tpl/abc" {
		t.Fatalf("spec template = %q", spec.Template)
	}
}

func TestStartSpecialProvisionsABuilder(t *testing.T) {
	h := newHarness(t, nil)
	tev := &fakeTemplateEvents{}
	h.c.d.TemplateEvents = tev
	ctx := context.Background()
	id, err := h.c.StartSpecial(ctx, SpecialSpec{Kind: store.KindBuild, Template: "tpl/abc", TemplateVMID: 951, Cores: 4, MemoryMB: 8192, DiskGB: 48,
		Env: map[string]string{ingest.EnvMode: ingest.ModeBuild}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ := h.db.GetEnvironment(ctx, id)
	if e.Kind != store.KindBuild || e.State != "booting" || e.MemoryMB != 8192 || e.ScaleSet != "" {
		t.Fatalf("builder = %+v", e)
	}
	spec, _ := h.rt.Spec(refOf(e))
	if spec.DiskGB != 48 || spec.Template != "tpl/abc" || spec.Env[ingest.EnvMode] != ingest.ModeBuild || spec.Env[ingest.EnvToken] == "" ||
		spec.Env[ingest.EnvURL] != "https://10.50.0.2:8443" || spec.Env[ingest.EnvJITConfig] != "" {
		t.Fatalf("spec = %+v", spec)
	}
	if got, ok := h.c.Resolve(ctx, ingest.HashToken(spec.Env[ingest.EnvToken])); !ok || got != id {
		t.Fatal("the builder token must resolve")
	}
	// Agent events of a builder reach the template service; hello still connects it.
	h.c.AgentEvent(ctx, id, ingest.EventHello, h.now, map[string]any{"ip": "10.50.0.9"})
	h.c.AgentEvent(ctx, id, ingest.EventBuildStep, h.now, map[string]any{"step": "clone"})
	if e, _ := h.db.GetEnvironment(ctx, id); e.State != "connected" {
		t.Fatalf("state = %s", e.State)
	}
	if len(tev.got) != 2 || tev.got[1] != id+":"+ingest.EventBuildStep {
		t.Fatalf("forwarded = %v", tev.got)
	}
	// Builders are not reaped by job timeouts (the template service has its own).
	h.now = h.now.Add(time.Hour)
	h.c.Reap(ctx)
	if e, _ := h.db.GetEnvironment(ctx, id); e.State != "connected" {
		t.Fatalf("after an hour a builder is %s; it must be left to the template service", e.State)
	}
	// And they never count as serving a scale set.
	h.c.Reconcile(ctx)
	if n := len(h.envs(t, "booting", "connected")); n != 1 {
		t.Fatalf("live = %d", n)
	}
}

func refOf(e store.Environment) runtime.Ref { return runtime.Ref{ID: e.RuntimeRef} }

func TestStuckBuildersStillHitLifecycleTimeouts(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	_ = h.db.CreateEnvironment(ctx, store.Environment{ID: "bld-stuck", Kind: store.KindBuild, State: "provisioning"})
	h.now = h.now.Add(time.Hour)
	h.c.Reap(ctx)
	if e, _ := h.db.GetEnvironment(ctx, "bld-stuck"); e.State == "provisioning" {
		t.Fatal("a builder stuck in provisioning must time out like any environment")
	}
}
