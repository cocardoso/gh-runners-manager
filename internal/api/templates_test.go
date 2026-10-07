package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/internal/template"
)

type fakeTemplates struct {
	running   bool
	built     int
	activated []string
	pinned    map[string]bool
	err       error
}

func (f *fakeTemplates) Build(context.Context, string) (store.Template, error) {
	if f.err != nil {
		return store.Template{}, f.err
	}
	f.built++
	return store.Template{ID: "new", State: store.TemplateBuilding}, nil
}
func (f *fakeTemplates) Activate(_ context.Context, id string) error {
	if f.err != nil {
		return f.err
	}
	f.activated = append(f.activated, id)
	return nil
}
func (f *fakeTemplates) Pin(_ context.Context, id string, p bool) error {
	if f.pinned == nil {
		f.pinned = map[string]bool{}
	}
	f.pinned[id] = p
	return f.err
}
func (f *fakeTemplates) Running(context.Context) bool               { return f.running }
func (f *fakeTemplates) InUse(context.Context, store.Template) bool { return true }
func (f *fakeTemplates) Enabled() bool                              { return true }

func TestTemplatesListAndActions(t *testing.T) {
	ft := &fakeTemplates{}
	h := newHarnessWith(t, "admin", func(d *Deps) { d.Templates = ft })
	ctx := context.Background()
	_ = h.db.CreateTemplate(ctx, store.Template{ID: "boot", State: store.TemplateActive, VMID: 949, Trigger: "bootstrap"})
	_ = h.db.CreateTemplate(ctx, store.Template{ID: "t2", State: store.TemplateReady, VMID: 951, RuntimeRef: "951/t2", SlimRelease: "20261005.17",
		RunnerVersion: "2.338.0", LayerVersion: "1", Report: []byte(`{"unexpected":1,"differences":[{"kind":"missing","name":"x"}]}`)})

	var list struct {
		Templates []Template `json:"templates"`
		Building  bool       `json:"building"`
		Enabled   bool       `json:"enabled"`
	}
	if code := h.getJSON(t, "/api/v1/templates", &list); code != 200 || len(list.Templates) != 2 || !list.Enabled {
		t.Fatalf("list = %d %+v", code, list)
	}
	byID := map[string]Template{}
	for _, tp := range list.Templates {
		byID[tp.ID] = tp
	}
	if !byID["boot"].Active || !byID["boot"].Bootstrap || byID["t2"].Active || !byID["t2"].InUse {
		t.Fatalf("flags = %+v", byID)
	}
	var rep map[string]any
	if err := json.Unmarshal(byID["t2"].Report, &rep); err != nil || rep["unexpected"].(float64) != 1 {
		t.Fatalf("report = %s", byID["t2"].Report)
	}
	var one Template
	if code := h.getJSON(t, "/api/v1/templates/t2", &one); code != 200 || one.SlimRelease != "20261005.17" {
		t.Fatalf("get = %d %+v", code, one)
	}
	if code := h.getJSON(t, "/api/v1/templates/nope", nil); code != 404 {
		t.Fatalf("missing = %d", code)
	}

	if resp, _ := h.call(t, "POST", "/api/v1/templates/build", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("build without credentials = %d", resp.StatusCode)
	}
	if code := post(t, h.srv.URL+"/api/v1/templates/build", "admin"); code != http.StatusAccepted || ft.built != 1 {
		t.Fatalf("build = %d", code)
	}
	if code := post(t, h.srv.URL+"/api/v1/templates/t2/activate", "admin"); code != http.StatusAccepted || ft.activated[0] != "t2" {
		t.Fatalf("activate = %d", code)
	}
	if code := post(t, h.srv.URL+"/api/v1/templates/t2/pin", "admin"); code != http.StatusNoContent || !ft.pinned["t2"] {
		t.Fatalf("pin = %d", code)
	}
	if code := post(t, h.srv.URL+"/api/v1/templates/t2/unpin", "admin"); code != http.StatusNoContent || ft.pinned["t2"] {
		t.Fatalf("unpin = %d", code)
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	audited := map[string]any{}
	for _, e := range evs {
		if strings.HasPrefix(e.Kind, "audit.template_") {
			audited[e.Kind] = e.Data["actor"]
		}
	}
	for _, k := range []string{"audit.template_build", "audit.template_activate", "audit.template_pin", "audit.template_unpin"} {
		if audited[k] != "token" {
			t.Errorf("%s actor = %v, want token (all: %v)", k, audited[k], audited)
		}
	}
	ft.err = template.ErrBuildRunning
	if code := post(t, h.srv.URL+"/api/v1/templates/build", "admin"); code != http.StatusConflict {
		t.Fatalf("build while running = %d, want 409", code)
	}
	ft.err = store.ErrNotFound
	if code := post(t, h.srv.URL+"/api/v1/templates/zzz/activate", "admin"); code != http.StatusNotFound {
		t.Fatalf("activate missing = %d", code)
	}
}
