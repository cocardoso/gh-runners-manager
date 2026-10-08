package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"
)

// ErrFirewallOpen means the job network's firewall did not apply in time.
var ErrFirewallOpen = errors.New("the job network firewall did not apply")

// probeConns is how many connections one probe round opens at once: all must time out to
// count as filtered, so one lost SYN on a busy bridge is no proof.
const probeConns = 3

// FirewallWait configures WaitFirewall.
type FirewallWait struct {
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error) // default: a 2 s dial
	Interval time.Duration                                                     // between rounds (default 500 ms)
	Limit    time.Duration                                                     // give up after (default 60 s)
	// Since and Settle are the fallback when the probe never answered: the fixed delay,
	// counted from Since (the agent's start, after the guest's).
	Since  time.Time
	Settle time.Duration
}

// WaitFirewall holds the runner until the job network's firewall applies to this guest.
// Proxmox applies a new guest's rules on pve-firewall's next cycle, up to ~10 s after it
// is configured. The probe is an IP address and a control-plane port the security group
// drops: while a connection to it succeeds or is refused, the guest is not filtered yet;
// once it has answered, a round where every connection times out means the group applies.
// A probe that never answered proves nothing (the rules may already apply, or something
// else drops it), so then the fixed delay is kept, counted from Since.
func WaitFirewall(ctx context.Context, probe string, o FirewallWait) error {
	// A name would be resolved on every dial, and a slow resolver would look like a drop.
	if ap, err := netip.ParseAddrPort(probe); err != nil || !ap.IsValid() {
		return fmt.Errorf("firewall probe %q must be an IP address and port", probe)
	}
	if o.Dial == nil {
		d := net.Dialer{Timeout: 2 * time.Second}
		o.Dial = d.DialContext
	}
	if o.Interval <= 0 {
		o.Interval = 500 * time.Millisecond
	}
	if o.Limit <= 0 {
		o.Limit = 60 * time.Second
	}
	if o.Since.IsZero() {
		o.Since = time.Now()
	}
	deadline := time.Now().Add(o.Limit)
	answered := false
	for {
		got := probeRound(ctx, o.Dial, probe)
		if err := ctx.Err(); err != nil {
			return err // a canceled dial is no timeout of the probe
		}
		switch {
		case got == roundFiltered && answered:
			return nil
		case got == roundFiltered:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Until(o.Since.Add(o.Settle))):
				return nil
			}
		case got == roundAnswered:
			answered = true
		}
		if time.Now().After(deadline) {
			if answered {
				return fmt.Errorf("%w within %s: %s still answers", ErrFirewallOpen, o.Limit, probe)
			}
			return fmt.Errorf("%w within %s: %s was never reachable", ErrFirewallOpen, o.Limit, probe)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.Interval):
		}
	}
}

type round int

const (
	roundUnclear  round = iota // neither answered nor all dropped (a network not up yet, a mix)
	roundAnswered              // a connection succeeded or was refused: the guest is not filtered
	roundFiltered              // every connection timed out
)

func probeRound(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), probe string) round {
	var wg sync.WaitGroup
	errs := make([]error, probeConns)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := dial(ctx, "tcp", probe)
			if conn != nil {
				_ = conn.Close()
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	timeouts := 0
	for _, err := range errs {
		switch {
		case err == nil || errors.Is(err, syscall.ECONNREFUSED):
			return roundAnswered
		case connectTimeout(err):
			timeouts++
		}
	}
	if timeouts == probeConns {
		return roundFiltered
	}
	return roundUnclear
}

// connectTimeout reports a TCP connect that timed out: a SYN nobody answered. A host
// unreachable is no proof (a missing route or a rebooting control plane says it too), and
// Proxmox's REJECT answers with a reset, so the group must DROP the probe.
func connectTimeout(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
