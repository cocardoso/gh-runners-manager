package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"
)

// EnvFirewallProbe tells a job environment's agent where to check that the job network's
// firewall applies before it starts the runner (see agent.WaitFirewall).
const EnvFirewallProbe = "GHRM_FIREWALL_PROBE"

// EnvFirewallSettle is the fixed firewall delay (a Go duration) the agent keeps when its
// probe never answered.
const EnvFirewallSettle = "GHRM_FIREWALL_SETTLE"

// EventFirewallOpen is sent by an agent whose firewall did not apply in time.
const EventFirewallOpen = "firewall_open"

// ProbeAddress is the firewall probe for an ingest URL: the same host, the next port.
// The job security group allows only the ingest port, so the probe is dropped.
func ProbeAddress(ingestURL string) (string, error) {
	u, err := url.Parse(ingestURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("ingest: no host in %q", ingestURL)
	}
	port := 443
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return "", fmt.Errorf("ingest: port of %q: %w", ingestURL, err)
		}
	}
	if port >= 65535 {
		return "", fmt.Errorf("ingest: port %d has no next port for the firewall probe", port)
	}
	return net.JoinHostPort(u.Hostname(), strconv.Itoa(port+1)), nil
}

// ListenProbe opens the firewall probe's port.
func ListenProbe(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

// ServeProbe accepts and closes connections until ctx ends: reaching it proves a guest
// is not filtered yet. Accept errors back off (5 ms doubling to 1 s), as net/http does,
// so running out of file descriptors does not spin.
func ServeProbe(ctx context.Context, ln net.Listener, logger *slog.Logger) {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	var delay time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			logger.Warn("firewall probe: accept failed", "error", err, "retry_in", delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			continue
		}
		delay = 0
		_ = conn.Close()
	}
}
