package controller

import (
	"context"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func lab() config.ScaleSet {
	return config.ScaleSet{Name: "lab", URL: "https://github.com/o/r", Credential: "c", MaxConcurrent: 4, Cores: 1, MemoryMB: 512}
}

func TestAddedScaleSetGetsEnvironments(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	added := config.ScaleSet{Name: "new", URL: "https://github.com/o/n", Credential: "c", MaxConcurrent: 2, Cores: 1, MemoryMB: 768}
	h.c.UpdateScaleSets([]config.ScaleSet{lab(), added})
	h.c.SetScaleSetID("new", 8)
	if _, err := h.c.Scaler("new").HandleDesiredRunnerCount(ctx, 1); err != nil {
		t.Fatal(err)
	}
	h.c.Wait()
	es, _ := h.db.ListEnvironments(ctx, store.EnvironmentFilter{ScaleSet: "new"})
	if len(es) != 1 || es[0].MemoryMB != 768 {
		t.Fatalf("environments of the new scale set = %+v", es)
	}
	if got := len(h.c.ScaleSets(ctx)); got != 2 {
		t.Fatalf("scale sets = %d, want 2", got)
	}
}

func TestChangedScaleSetUsesTheNewSizeForNewEnvironments(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	first := h.provision(t, 1)[0]
	bigger := lab()
	bigger.MemoryMB = 2048
	h.c.UpdateScaleSets([]config.ScaleSet{bigger})
	h.provision(t, 2)
	es, _ := h.db.ListEnvironments(ctx, store.EnvironmentFilter{ScaleSet: "lab"})
	sizes := map[int]int{}
	for _, e := range es {
		sizes[e.MemoryMB]++
	}
	if len(es) != 2 || sizes[512] != 1 || sizes[2048] != 1 {
		t.Fatalf("environments = %+v; want the running one untouched (%s) and the new one bigger", es, first.ID)
	}
}

func TestRemovedScaleSetDrains(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.c.UpdateScaleSets(nil)
	sets := h.c.ScaleSets(ctx)
	if len(sets) != 1 || !sets[0].Removed || sets[0].Live != 1 {
		t.Fatalf("scale sets = %+v; want lab listed as removed while it has a live environment", sets)
	}
	if _, err := h.c.Scaler("lab").HandleDesiredRunnerCount(ctx, 3); err != nil {
		t.Fatal(err)
	}
	h.c.Wait()
	if n := len(h.envs(t)); n != 1 {
		t.Fatalf("environments = %d; a removed scale set gets no new ones", n)
	}
	if got, _ := h.db.GetEnvironment(ctx, e.ID); got.State == "destroyed" {
		t.Fatal("the live environment must be left to finish")
	}
	h.c.Fail(ctx, e.ID, "test", context.Canceled)
	h.c.Teardown(ctx)
	if sets := h.c.ScaleSets(ctx); len(sets) != 0 {
		t.Fatalf("scale sets = %+v; want it gone once drained", sets)
	}
	// Adding it back revives it.
	h.c.UpdateScaleSets([]config.ScaleSet{lab()})
	if sets := h.c.ScaleSets(ctx); len(sets) != 1 || sets[0].Removed {
		t.Fatalf("re-added = %+v", sets)
	}
}

// A reconcile that planned an environment before the scale set was removed (or even
// pruned) must neither crash nor create it.
func TestProvisioningSkipsARemovedScaleSet(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.c.UpdateScaleSets(nil)
	if err := h.c.startProvisioning(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	h.c.mu.Lock()
	delete(h.c.scaleSets, "lab")
	h.c.order = nil
	h.c.mu.Unlock()
	if err := h.c.startProvisioning(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	h.c.Wait()
	if n := len(h.envs(t)); n != 0 {
		t.Fatalf("environments = %d, want none for a removed scale set", n)
	}
}

// Draining scale sets keep their settings (the listener stays up, so job messages and
// runner removal still work) until their last environment is gone.
func TestDrainingKeepsRemovedScaleSetsUntilEmpty(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e := h.provision(t, 1)[0]
	h.c.UpdateScaleSets(nil)
	_ = h.c.ScaleSets(ctx) // a UI poll must not forget it
	d := h.c.Draining(ctx)
	if len(d) != 1 || d[0].Name != "lab" || d[0].URL == "" {
		t.Fatalf("draining = %+v; want lab with its settings", d)
	}
	h.c.Fail(ctx, e.ID, "test", context.Canceled)
	h.c.Teardown(ctx)
	if d := h.c.Draining(ctx); len(d) != 0 {
		t.Fatalf("draining = %+v; want none once empty", d)
	}
	if s := h.c.ScaleSets(ctx); len(s) != 0 {
		t.Fatalf("scale sets = %+v", s)
	}
}
