package main

import (
	"context"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/agent"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

func TestGateReportsAFirewallThatNeverApplied(t *testing.T) {
	var events []string
	ok := gateRunner(context.Background(), func(context.Context) error { return agent.ErrFirewallOpen },
		func(string, string) {}, func(name string, _ map[string]any) { events = append(events, name) })
	if ok || len(events) != 1 || events[0] != ingest.EventFirewallOpen {
		t.Fatalf("ok=%v events=%v, want a firewall_open and no runner", ok, events)
	}
}

func TestGateStoppedGuestReportsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var events []string
	ok := gateRunner(ctx, func(ctx context.Context) error { return ctx.Err() },
		func(string, string) {}, func(name string, _ map[string]any) { events = append(events, name) })
	if ok || len(events) != 0 {
		t.Fatalf("ok=%v events=%v, want no runner and no event", ok, events)
	}
}
