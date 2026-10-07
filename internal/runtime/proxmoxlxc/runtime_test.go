package proxmoxlxc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox/proxmoxtest"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

var _ runtime.Runtime = (*Runtime)(nil)

type harness struct {
	srv    *proxmoxtest.Server
	rt     *Runtime
	sleeps []time.Duration
}

func newHarness(t *testing.T, start, end int) *harness {
	t.Helper()
	srv := proxmoxtest.NewServer(t, "pve", "ghrm@pve!ghrm", "s3cret")
	srv.AddGuest(proxmoxtest.Guest{VMID: 9000, Type: "lxc", Name: "ghrm-template", Template: true, Tags: "ghrm-template", Config: map[string]string{"memory": "4096"}})
	client, err := proxmox.New(proxmox.Config{URL: srv.URL, TokenID: "ghrm@pve!ghrm", TokenSecret: "s3cret", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	client.PollInterval = time.Millisecond
	h := &harness{srv: srv}
	h.rt = New(client, Config{Node: "pve", TemplateVMID: 9000, Pool: "ghrm", VMIDStart: start, VMIDEnd: end, ThinPool: "data", FirewallSettle: 12 * time.Second})
	h.rt.SetSleep(func(ctx context.Context, d time.Duration) error {
		h.sleeps = append(h.sleeps, d)
		return ctx.Err()
	})
	return h
}

func spec(id string) runtime.EnvironmentSpec {
	return runtime.EnvironmentSpec{
		ID: id, Hostname: "ghrm-" + id, Cores: 2, MemoryMB: 4096,
		Env: map[string]string{"GHRM_JITCONFIG": "abc==", "GHRM_ENV_ID": id},
	}
}

func TestCreateConfiguresCloneAndWaitsForFirewall(t *testing.T) {
	h := newHarness(t, 900, 909)
	ref, err := h.rt.Create(context.Background(), spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "900" {
		t.Fatalf("ref = %v, want 900", ref)
	}
	g, _ := h.srv.Guest(900)
	want := map[string]string{
		"cores": "2", "memory": "4096", "swap": "0", "pool": "ghrm", "linked": "0",
		"hostname": "ghrm-aaa", "env": "GHRM_ENV_ID=aaa\x00GHRM_JITCONFIG=abc==",
	}
	for k, v := range want {
		if g.Config[k] != v {
			t.Errorf("config[%s] = %q, want %q", k, g.Config[k], v)
		}
	}
	if g.Tags != "ghrm-env;ghrmid-aaa" {
		t.Errorf("tags = %q", g.Tags)
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 12*time.Second {
		t.Errorf("sleeps = %v, want one 12s firewall settle", h.sleeps)
	}
}

func TestCreateIsIdempotentPerID(t *testing.T) {
	h := newHarness(t, 900, 909)
	ctx := context.Background()
	first, err := h.rt.Create(ctx, spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.rt.Create(ctx, spec("aaa"))
	if err != nil || second != first {
		t.Fatalf("second Create = %v, %v; want %v", second, err, first)
	}
	clones := 0
	for _, r := range h.srv.Requests() {
		if strings.HasSuffix(r, "/clone") {
			clones++
		}
	}
	if clones != 1 {
		t.Fatalf("clone requests = %d, want 1", clones)
	}
}

func TestCreateSkipsVMIDsTakenOutsidePool(t *testing.T) {
	h := newHarness(t, 900, 909)
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 900, Type: "qemu", Name: "someone-elses-vm"})
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 901, Type: "lxc", Name: "unrelated"})
	ref, err := h.rt.Create(context.Background(), spec("aaa"))
	if err != nil || ref.ID != "902" {
		t.Fatalf("Create = %v, %v; want 902", ref, err)
	}
}

func TestCreateFailsWhenRangeExhausted(t *testing.T) {
	h := newHarness(t, 900, 901)
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 900, Type: "lxc"})
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 901, Type: "qemu"})
	_, err := h.rt.Create(context.Background(), spec("aaa"))
	if !errors.Is(err, ErrNoFreeVMID) {
		t.Fatalf("err = %v, want ErrNoFreeVMID", err)
	}
}

func TestCreateCleansUpWhenConfigFails(t *testing.T) {
	h := newHarness(t, 900, 909)
	h.srv.FailConfigPut = true
	if _, err := h.rt.Create(context.Background(), spec("aaa")); err == nil {
		t.Fatal("want error")
	}
	if _, exists := h.srv.Guest(900); exists {
		t.Fatal("clone leaked after config failure")
	}
}

func TestCreateCleansUpWhenCancelledDuringSettle(t *testing.T) {
	h := newHarness(t, 900, 909)
	ctx, cancel := context.WithCancel(context.Background())
	h.rt.SetSleep(func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	})
	if _, err := h.rt.Create(ctx, spec("aaa")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, exists := h.srv.Guest(900); exists {
		t.Fatal("clone leaked after cancellation")
	}
}

func TestCreateRejectsInvalidSpecWithoutAPICalls(t *testing.T) {
	h := newHarness(t, 900, 909)
	bad := spec("aaa")
	bad.Env["GHRM_X"] = "line\nbreak"
	if _, err := h.rt.Create(context.Background(), bad); !errors.Is(err, runtime.ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
	if n := len(h.srv.Requests()); n != 0 {
		t.Fatalf("made %d API calls for an invalid spec", n)
	}
}

func TestStartStatusListDestroy(t *testing.T) {
	h := newHarness(t, 900, 909)
	ctx := context.Background()
	ref, err := h.rt.Create(ctx, spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.rt.Start(ctx, ref); err != nil {
		t.Fatal(err)
	}
	st, err := h.rt.Status(ctx, ref)
	if err != nil || !st.Running || st.EnvironmentID != "aaa" || st.IP != "10.50.0.100" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	list, err := h.rt.List(ctx)
	if err != nil || len(list) != 1 || list[0].EnvironmentID != "aaa" || list[0].Ref != ref {
		t.Fatalf("List = %+v, %v (the template must not be listed)", list, err)
	}
	if err := h.rt.Destroy(ctx, ref); err != nil {
		t.Fatalf("Destroy of a running environment = %v", err)
	}
	if _, err := h.rt.Status(ctx, ref); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Status after Destroy = %v, want ErrNotFound", err)
	}
}

func TestDestroyMissingIsNoop(t *testing.T) {
	h := newHarness(t, 900, 909)
	if err := h.rt.Destroy(context.Background(), runtime.Ref{ID: "905"}); err != nil {
		t.Fatalf("Destroy(missing) = %v, want nil", err)
	}
	if err := h.rt.Stop(context.Background(), runtime.Ref{ID: "905"}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Stop(missing) = %v, want ErrNotFound", err)
	}
}

func TestRejectsMalformedRef(t *testing.T) {
	h := newHarness(t, 900, 909)
	if _, err := h.rt.Status(context.Background(), runtime.Ref{ID: "abc"}); err == nil {
		t.Fatal("want error for non-numeric ref")
	}
}

func TestCapacity(t *testing.T) {
	h := newHarness(t, 900, 909)
	h.srv.MemoryTotal, h.srv.MemoryAvailable = 40<<30, 20<<30
	h.srv.ThinPools = []proxmoxtest.ThinPool{
		{LV: "other", Size: 100, Used: 99},
		{LV: "data", Size: 1000, Used: 400, MetadataSize: 100, MetadataUsed: 60},
	}
	if _, err := h.rt.Create(context.Background(), spec("aaa")); err != nil {
		t.Fatal(err)
	}
	c, err := h.rt.Capacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.HostMemoryTotalMB != 40*1024 || c.HostMemoryAvailableMB != 20*1024 || c.ThinPoolPercent != 60 || c.Environments != 1 {
		t.Fatalf("Capacity = %+v", c)
	}
}
