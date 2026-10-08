package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
)

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func TestFirewallProbeServesNextToTheIngest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The probe takes the ingest port + 1; find a pair that is free.
	var probe string
	for range 20 {
		port := freePort(t)
		in := config.Ingest{Listen: "127.0.0.1:" + port, AdvertiseURL: "https://127.0.0.1:" + port}
		if probe = serveFirewallProbe(ctx, in, log); probe != "" {
			break
		}
	}
	if probe == "" {
		t.Fatal("no probe served")
	}
	conn, err := net.DialTimeout("tcp", probe, time.Second)
	if err != nil {
		t.Fatalf("dial %s = %v", probe, err)
	}
	_ = conn.Close()

	if got := serveFirewallProbe(ctx, config.Ingest{Listen: "127.0.0.1:9443", AdvertiseURL: "https://10.50.0.2:8443"}, log); got != "" {
		t.Fatalf("ingest advertised on another port: probe = %q, want none", got)
	}
}
