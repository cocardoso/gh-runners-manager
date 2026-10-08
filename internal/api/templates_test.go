package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/internal/template"
)

type fakeTemplates struct {
	running   bool
	built     int
	builtFor  []string
	activated []string
	pinned    map[string]bool
	profiles  []template.Profile
	deleted   []string
	err       error
}

func (f *fakeTemplates) Build(_ context.Context, _, profile string) (store.Template, error) {
	if f.err != nil {
		return store.Template{}, f.err
	}
	f.built++
	f.builtFor = append(f.builtFor, profile)
	return store.Template{ID: "new", State: store.TemplateBuilding, Profile: profile}, nil
}
func (f *fakeTemplates) Profiles(context.Context) ([]template.Profile, error) {
	return append([]template.Profile{template.DefaultProfile()}, f.profiles...), nil
}
func (f *fakeTemplates) PutProfile(_ context.Context, p template.Profile) error {
	f.profiles = append(f.profiles, p.Normalize())
	return nil
}
func (f *fakeTemplates) DeleteProfile(_ context.Context, name string) error {
	if name == "default" {
		return template.ErrDefaultProfile
	}
	f.deleted = append(f.deleted, name)
	return nil
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

func TestTemplateProfileEndpoints(t *testing.T) {
	ft := &fakeTemplates{}
	h := newHarnessWith(t, "admin", func(d *Deps) { d.Templates = ft })
	admin := map[string]string{"Authorization": "Bearer admin"}

	// Building a profile names it; without a body the default profile is built.
	if resp, b := h.call(t, "POST", "/api/v1/templates/build", map[string]string{"profile": "lean"}, admin); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("build lean = %d %s", resp.StatusCode, b)
	}
	if code := post(t, h.srv.URL+"/api/v1/templates/build", "admin"); code != http.StatusAccepted || strings.Join(ft.builtFor, ",") != "lean," {
		t.Fatalf("builds = %d %v", code, ft.builtFor)
	}

	body := map[string]any{"remove": []string{"aws-tools"}, "toolcache": map[string][]string{"node": {"22"}}, "apt": []string{"zip"}}
	if resp, b := h.call(t, "PUT", "/api/v1/template-profiles/lean", body, admin); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/template-profiles/bad", map[string]any{"remove": []string{"docker-cli"}}, admin); resp.StatusCode != 422 {
		t.Fatalf("invalid profile = %d, want 422", resp.StatusCode)
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/template-profiles/lean", body, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("put without credentials = %d", resp.StatusCode)
	}
	var list struct {
		Profiles   []TemplateProfile `json:"profiles"`
		Components []map[string]any  `json:"components"`
	}
	if code := h.getJSON(t, "/api/v1/template-profiles", &list); code != 200 || len(list.Profiles) != 2 || len(list.Components) == 0 {
		t.Fatalf("list = %d %+v", code, list)
	}
	if def := list.Profiles[0]; def.Name != "default" || strings.Join(def.Toolcache["node"], ",") != "22,24" || def.UsedBy == nil {
		t.Fatalf("default profile = %+v", def)
	}

	if resp, _ := h.call(t, "DELETE", "/api/v1/template-profiles/default", nil, admin); resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete default = %d, want 409", resp.StatusCode)
	}
	if resp, _ := h.call(t, "DELETE", "/api/v1/template-profiles/lean", nil, admin); resp.StatusCode != http.StatusNoContent || ft.deleted[0] != "lean" {
		t.Fatalf("delete lean = %d", resp.StatusCode)
	}
}

func TestATemplateProfileInUseIsNotDeleted(t *testing.T) {
	ft := &fakeTemplates{}
	h := newHarnessWith(t, "admin", func(d *Deps) {
		d.Templates = ft
		d.Config.ScaleSets = append(d.Config.ScaleSets, config.ScaleSet{Name: "fast", TemplateProfile: "lean"})
	})
	resp, b := h.call(t, "DELETE", "/api/v1/template-profiles/lean", nil, map[string]string{"Authorization": "Bearer admin"})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), "fast") {
		t.Fatalf("delete a used profile = %d %s, want 409 naming the scale set", resp.StatusCode, b)
	}
}

func TestCreatingATemplateProfileNeverReplacesOne(t *testing.T) {
	ft := &fakeTemplates{profiles: []template.Profile{{Name: "lean"}}}
	h := newHarnessWith(t, "admin", func(d *Deps) { d.Templates = ft })
	create := map[string]string{"Authorization": "Bearer admin", "If-None-Match": "*"}
	for _, name := range []string{"lean", "default"} {
		if resp, _ := h.call(t, "PUT", "/api/v1/template-profiles/"+name, map[string]any{}, create); resp.StatusCode != http.StatusPreconditionFailed {
			t.Errorf("create %s = %d, want 412", name, resp.StatusCode)
		}
	}
	if resp, b := h.call(t, "PUT", "/api/v1/template-profiles/fresh", map[string]any{}, create); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("create fresh = %d %s", resp.StatusCode, b)
	}
}

func TestAScaleSetNeedsAnExistingTemplateProfile(t *testing.T) {
	ft := &fakeTemplates{profiles: []template.Profile{{Name: "lean"}}}
	h := newHarnessWith(t, "admin", func(d *Deps) { d.Templates = ft })
	admin := map[string]string{"Authorization": "Bearer admin"}
	body := map[string]any{"url": "https://github.com/o/r", "credential": "c", "template_profile": "typo"}
	if resp, b := h.call(t, "PUT", "/api/v1/scale-sets/x", body, admin); resp.StatusCode != 422 || !strings.Contains(string(b), "typo") {
		t.Fatalf("unknown profile = %d %s, want 422", resp.StatusCode, b)
	}
	body["template_profile"] = "lean"
	if resp, b := h.call(t, "PUT", "/api/v1/scale-sets/x", body, admin); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("known profile = %d %s", resp.StatusCode, b)
	}
}
