package controller

import (
	"context"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

type gatedSource struct{ gated bool }

func (gatedSource) Active(context.Context, string) (string, int) { return "tpl/abc", 951 }
func (g gatedSource) ActiveFirewallGated(context.Context, string) (string, int, bool) {
	return "tpl/abc", 951, g.gated
}

func TestGatedTemplateGetsTheProbeAndNoSettleDelay(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.Proxmox.FirewallSettle = config.Duration(12 * time.Second) })
	h.c.d.Templates = gatedSource{gated: true}
	h.c.d.FirewallProbe = "10.50.0.2:8444"
	e := h.provision(t, 1)[0]
	spec, _ := h.rt.Spec(refOf(e))
	if spec.Env[ingest.EnvFirewallSettle] != "12s" {
		t.Fatalf("settle = %q, want 12s for the agent's fallback", spec.Env[ingest.EnvFirewallSettle])
	}
	if !spec.FirewallGated || spec.Env[ingest.EnvFirewallProbe] != "10.50.0.2:8444" {
		t.Fatalf("spec gated=%v probe=%q, want gated with the probe", spec.FirewallGated, spec.Env[ingest.EnvFirewallProbe])
	}
}

func TestOlderTemplatesKeepTheSettleDelay(t *testing.T) {
	for name, setup := range map[string]func(h *harness){
		"template without the gate": func(h *harness) {
			h.c.d.Templates = gatedSource{gated: false}
			h.c.d.FirewallProbe = "10.50.0.2:8444"
		},
		"no probe listener":   func(h *harness) { h.c.d.Templates = gatedSource{gated: true} },
		"no template service": func(h *harness) { h.c.d.FirewallProbe = "10.50.0.2:8444" },
	} {
		h := newHarness(t, nil)
		setup(h)
		e := h.provision(t, 1)[0]
		spec, _ := h.rt.Spec(refOf(e))
		if spec.FirewallGated || spec.Env[ingest.EnvFirewallProbe] != "" {
			t.Errorf("%s: gated=%v probe=%q, want the settle delay", name, spec.FirewallGated, spec.Env[ingest.EnvFirewallProbe])
		}
	}
}

func TestFirewallThatNeverAppliesFailsTheEnvironment(t *testing.T) {
	h := newHarness(t, nil)
	e := h.provision(t, 1)[0]
	ctx := context.Background()
	h.c.AgentEvent(ctx, e.ID, ingest.EventHello, time.Now(), nil)
	h.c.AgentEvent(ctx, e.ID, ingest.EventFirewallOpen, time.Now(), map[string]any{"error": "10.50.0.2:8444 still answers"})
	got, _ := h.db.GetEnvironment(ctx, e.ID)
	if got.FailureStage != "firewall" {
		t.Fatalf("env = %s / %q, want failed at firewall", got.State, got.FailureStage)
	}
}
