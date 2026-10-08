package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTemplateRoundTripAndActivation(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	a := Template{ID: "tpla", SlimRelease: "20261005.17", RunnerVersion: "2.338.0", LayerVersion: "1", State: TemplateActive,
		VMID: 950, Trigger: "bootstrap", Report: []byte(`{"unexpected":0}`), Pinned: true, FirewallGate: true}
	b := Template{ID: "tplb", SlimRelease: "20261012.3", RunnerVersion: "2.339.0", LayerVersion: "1", State: TemplateReady, VMID: 951, RuntimeRef: "951/tplb", SizeBytes: 123}
	for _, tpl := range []Template{a, b} {
		if err := s.CreateTemplate(ctx, tpl); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetTemplate(ctx, "tpla")
	if err != nil || string(got.Report) != `{"unexpected":0}` || !got.Pinned || !got.FirewallGate || got.VMID != 950 || got.CreatedAt.IsZero() {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if _, err := s.GetTemplate(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v, want ErrNotFound", err)
	}
	active, err := s.ActiveTemplate(ctx, DefaultProfile)
	if err != nil || active.ID != "tpla" {
		t.Fatalf("active = %+v, %v", active, err)
	}
	at := time.Now()
	if err := s.SetActiveTemplate(ctx, "tplb", at); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.ActiveTemplate(ctx, DefaultProfile); active.ID != "tplb" || active.ActivatedAt.IsZero() {
		t.Fatalf("after switch active = %+v", active)
	}
	if prev, _ := s.GetTemplate(ctx, "tpla"); prev.State != TemplateReady {
		t.Fatalf("previous active state = %s, want ready", prev.State)
	}
	b2, _ := s.GetTemplate(ctx, "tplb")
	b2.State = TemplateActive
	b2.ID = "tplc"
	if err := s.CreateTemplate(ctx, b2); err == nil {
		t.Fatal("a second active template must be rejected")
	}
	if g, _ := s.GetTemplate(ctx, "tplb"); g.RuntimeRef != "951/tplb" {
		t.Fatalf("runtime ref = %q", g.RuntimeRef)
	}
	list, _ := s.ListTemplates(ctx)
	if len(list) != 2 || list[0].ID != "tplb" {
		t.Fatalf("list = %+v, want newest first", list)
	}
	got.FailureReason = "x"
	got.State = TemplateFailed
	if err := s.UpdateTemplate(ctx, got); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetTemplate(ctx, "tpla"); g.State != TemplateFailed || g.FailureReason != "x" {
		t.Fatalf("update = %+v", g)
	}
	if err := s.SetActiveTemplate(ctx, "nope", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("activate missing = %v", err)
	}
}

func TestActiveTemplateNone(t *testing.T) {
	if _, err := openTemp(t).ActiveTemplate(context.Background(), DefaultProfile); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnvironmentKindsAndTemplate(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	_ = s.CreateEnvironment(ctx, Environment{ID: "j1", ScaleSet: "lab", State: "running", TemplateVMID: 950})
	_ = s.CreateEnvironment(ctx, Environment{ID: "b1", State: "running", Kind: KindBuild, TemplateVMID: 950})
	j, _ := s.GetEnvironment(ctx, "j1")
	if j.Kind != KindJob || j.TemplateVMID != 950 {
		t.Fatalf("job env = %+v, want kind job by default", j)
	}
	only, _ := s.ListEnvironments(ctx, EnvironmentFilter{Kinds: []string{KindBuild}})
	if len(only) != 1 || only[0].ID != "b1" {
		t.Fatalf("filter by kind = %+v", only)
	}
	all, _ := s.ListEnvironments(ctx, EnvironmentFilter{})
	if len(all) != 2 {
		t.Fatalf("all = %d", len(all))
	}
}

func TestOneActiveTemplatePerProfile(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	for _, tpl := range []Template{
		{ID: "d1", State: TemplateReady, VMID: 950},
		{ID: "n1", State: TemplateReady, VMID: 951, Profile: "node"},
		{ID: "n2", State: TemplateReady, VMID: 952, Profile: "node"},
	} {
		if err := s.CreateTemplate(ctx, tpl); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"d1", "n1", "n2"} {
		if err := s.SetActiveTemplate(ctx, id, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if a, err := s.ActiveTemplate(ctx, DefaultProfile); err != nil || a.ID != "d1" || a.Profile != DefaultProfile {
		t.Fatalf("default active = %+v, %v", a, err)
	}
	if a, err := s.ActiveTemplate(ctx, "node"); err != nil || a.ID != "n2" {
		t.Fatalf("node active = %+v, %v", a, err)
	}
	if n1, _ := s.GetTemplate(ctx, "n1"); n1.State != TemplateReady {
		t.Fatalf("n1 = %s, want ready after n2 took over its profile", n1.State)
	}
}

func TestTemplateProfilesRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.PutTemplateProfile(ctx, "node", []byte(`{"remove":["azure-cli"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.PutTemplateProfile(ctx, "node", []byte(`{"remove":["aws-tools"]}`)); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetTemplateProfile(ctx, "node")
	if err != nil || string(p.Spec) != `{"remove":["aws-tools"]}` || p.CreatedAt.IsZero() {
		t.Fatalf("profile = %+v, %v", p, err)
	}
	if list, _ := s.ListTemplateProfiles(ctx); len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	if err := s.DeleteTemplateProfile(ctx, "node"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTemplateProfile(ctx, "node"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
