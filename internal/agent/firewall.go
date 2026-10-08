package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

// ErrFirewallOpen means the job network's firewall did not apply in time.
var ErrFirewallOpen = errors.New("the job network firewall did not apply")

// FirewallWait configures WaitFirewall.
type FirewallWait struct {
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error) // default: a 1 s dial
	Interval time.Duration                                                     // between probes (default 500 ms)
	Limit    time.Duration                                                     // give up after (default 60 s)
	// Since and Settle are the fallback when the probe never answered: the fixed delay,
	// counted from Since (the agent's start, after the guest's).
	Since  time.Time
	Settle time.Duration
}

// WaitFirewall holds the runner until the job network's firewall applies to this guest.
// Proxmox applies a new guest's rules on pve-firewall's next cycle, up to ~10 s after it
// is configured. The probe address is a control-plane port the security group drops:
// while a connection to it succeeds or is refused, the guest is not filtered yet; once it
// has answered, a timeout or an unreachable host means the group applies. A probe that
// never answered proves nothing (the rules may already apply, or something else drops
// it), so then the fixed delay is kept, counted from Since.
func WaitFirewall(ctx context.Context, probe string, o FirewallWait) error {
	if o.Dial == nil {
		d := net.Dialer{Timeout: time.Second}
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
		conn, err := o.Dial(ctx, "tcp", probe)
		if conn != nil {
			_ = conn.Close()
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr // a canceled dial is no timeout of the probe
		}
		if filtered(err) {
			if answered {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Until(o.Since.Add(o.Settle))):
				return nil
			}
		}
		answered = answered || err == nil || errors.Is(err, syscall.ECONNREFUSED)
		if time.Now().After(deadline) {
			return fmt.Errorf("%w within %s (%s still answers)", ErrFirewallOpen, o.Limit, probe)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.Interval):
		}
	}
}

func filtered(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, syscall.EHOSTUNREACH)
}
