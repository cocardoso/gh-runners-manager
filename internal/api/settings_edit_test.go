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
