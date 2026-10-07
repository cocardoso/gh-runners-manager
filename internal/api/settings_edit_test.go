package api

import (
	"context"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func TestCredentialEndpointsNeverReturnTheToken(t *testing.T) {
	h := newHarness(t, "")
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	if resp, b := h.call(t, "PUT", "/api/v1/credentials/home", map[string]string{"token": "github_pat_good"}, tok); resp.StatusCode != 204 {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	resp, b := h.call(t, "GET", "/api/v1/credentials", nil, tok)
	body := string(b)
	if resp.StatusCode != 200 || strings.Contains(body, "github_pat_good") || !strings.Contains(body, `"name":"home"`) ||
		!strings.Contains(body, `"source":"ui"`) || !strings.Contains(body, `"token_hint":"good"`) || !strings.Contains(body, `"name":"c"`) {
		t.Fatalf("list = %d %s", resp.StatusCode, body)
	}
	_, b = h.call(t, "POST", "/api/v1/credentials/home/test", nil, tok)
	if !strings.Contains(string(b), `"ok":true`) || !strings.Contains(string(b), "octocat") {
		t.Fatalf("test = %s", b)
	}
	_ = h.reg.PutCredential(context.Background(), "bad", "github_pat_bad")
	if _, b = h.call(t, "POST", "/api/v1/credentials/bad/test", nil, tok); !strings.Contains(string(b), `"ok":false`) || !strings.Contains(string(b), "Bad credentials") {
		t.Fatalf("bad test = %s", b)
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/credentials/c", map[string]string{"token": "x"}, tok); resp.StatusCode != 409 {
		t.Fatalf("file credential put = %d, want 409", resp.StatusCode)
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/credentials/Bad%20Name", map[string]string{"token": "x"}, tok); resp.StatusCode != 422 {
		t.Fatalf("bad name = %d, want 422", resp.StatusCode)
	}
	_ = h.reg.PutScaleSet(context.Background(), config.ScaleSet{Name: "ui-ss", URL: "https://github.com/o", Credential: "home"})
	if resp, _ := h.call(t, "DELETE", "/api/v1/credentials/home", nil, tok); resp.StatusCode != 409 {
		t.Fatalf("delete in use = %d, want 409", resp.StatusCode)
	}
	if resp, _ := h.call(t, "DELETE", "/api/v1/credentials/bad", nil, tok); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp, _ := h.call(t, "DELETE", "/api/v1/credentials/bad", nil, tok); resp.StatusCode != 404 {
		t.Fatalf("delete again = %d, want 404", resp.StatusCode)
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Kind]++
	}
	if kinds["audit.credential_put"] != 1 || kinds["audit.credential_delete"] != 1 {
		t.Fatalf("audit = %v", kinds)
	}
}

func TestScaleSetEndpoints(t *testing.T) {
	h := newHarness(t, "")
	tok := map[string]string{"Authorization": "Bearer " + harnessToken}
	body := map[string]any{"url": "https://github.com/o/new", "credential": "c", "labels": []string{"big"}, "memory_mb": 2048}
	if resp, b := h.call(t, "PUT", "/api/v1/scale-sets/new-set", body, tok); resp.StatusCode != 204 {
		t.Fatalf("put = %d %s", resp.StatusCode, b)
	}
	ss, ok := h.reg.ScaleSet("new-set")
	if !ok || ss.MemoryMB != 2048 || ss.Cores != 2 || ss.Labels[0] != "big" {
		t.Fatalf("stored = %+v %v", ss, ok)
	}
	for name, bad := range map[string]map[string]any{
		"bad url":            {"url": "https://example.com/o", "credential": "c"},
		"unknown credential": {"url": "https://github.com/o", "credential": "nope"},
	} {
		if resp, _ := h.call(t, "PUT", "/api/v1/scale-sets/x", bad, tok); resp.StatusCode != 422 {
			t.Errorf("%s = %d, want 422", name, resp.StatusCode)
		}
	}
	if resp, _ := h.call(t, "PUT", "/api/v1/scale-sets/lab", body, tok); resp.StatusCode != 409 {
		t.Fatalf("file scale set = %d, want 409", resp.StatusCode)
	}
	_, b := h.call(t, "GET", "/api/v1/scale-sets", nil, tok)
	if !strings.Contains(string(b), `"source":"file"`) || !strings.Contains(string(b), `"url":"https://github.com/o/r"`) {
		t.Fatalf("list = %s", b)
	}
	if resp, _ := h.call(t, "DELETE", "/api/v1/scale-sets/new-set", nil, tok); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if _, ok := h.reg.ScaleSet("new-set"); ok {
		t.Fatal("still there")
	}
	evs, _ := h.db.ListEvents(context.Background(), store.EventFilter{})
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Kind]++
	}
	if kinds["audit.scale_set_put"] != 1 || kinds["audit.scale_set_delete"] != 1 {
		t.Fatalf("audit = %v", kinds)
	}
}
