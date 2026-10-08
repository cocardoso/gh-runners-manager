package agent

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var (
	dropped  = &net.OpError{Op: "dial", Err: timeoutErr{}}
	refused  = &net.OpError{Op: "dial", Err: &osSyscallErr{syscall.ECONNREFUSED}}
	noRoute  = &net.OpError{Op: "dial", Err: &osSyscallErr{syscall.EHOSTUNREACH}}
	netDown  = &net.OpError{Op: "dial", Err: &osSyscallErr{syscall.ENETUNREACH}}
	slowName = &net.OpError{Op: "dial", Err: &net.DNSError{Err: "timeout", Name: "ingest.lan", IsTimeout: true}}
)

type osSyscallErr struct{ errno syscall.Errno }

func (e *osSyscallErr) Error() string { return e.errno.Error() }
func (e *osSyscallErr) Unwrap() error { return e.errno }

// rounds answers each probe round with the results given for it (one per connection, the
// last repeated), then repeats the last round.
func rounds(results ...[]error) (func(context.Context, string, string) (net.Conn, error), *int) {
	var mu sync.Mutex
	dials := 0
	return func(context.Context, string, string) (net.Conn, error) {
		mu.Lock()
		r := results[min(dials/probeConns, len(results)-1)]
		e := r[min(dials%probeConns, len(r)-1)]
		dials++
		mu.Unlock()
		if e == nil {
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		}
		return nil, e
	}, &dials
}

func wait(dial func(context.Context, string, string) (net.Conn, error), limit time.Duration) error {
	return WaitFirewall(context.Background(), "10.50.0.2:8444", FirewallWait{Dial: dial, Interval: time.Millisecond, Limit: limit})
}

func TestWaitFirewallStartsOnceTheProbeIsDropped(t *testing.T) {
	dial, dials := rounds([]error{nil}, []error{refused}, []error{dropped})
	if err := wait(dial, time.Second); err != nil {
		t.Fatalf("WaitFirewall = %v, want nil once every probe times out", err)
	}
	if *dials != 3*probeConns {
		t.Fatalf("dials = %d, want 3 rounds of %d", *dials, probeConns)
	}
}

// One SYN lost on a busy bridge must not start the runner.
func TestWaitFirewallNeedsEveryConnectionOfARoundDropped(t *testing.T) {
	dial, _ := rounds([]error{nil}, []error{dropped, nil, dropped})
	if err := wait(dial, 20*time.Millisecond); !errors.Is(err, ErrFirewallOpen) {
		t.Fatalf("WaitFirewall = %v, want ErrFirewallOpen", err)
	}
}

func TestWaitFirewallTakesNoUnreachableHostOrSlowResolverForADrop(t *testing.T) {
	for name, e := range map[string]error{"host unreachable": noRoute, "network down": netDown, "slow resolver": slowName} {
		dial, _ := rounds([]error{nil}, []error{e})
		if err := wait(dial, 20*time.Millisecond); !errors.Is(err, ErrFirewallOpen) {
			t.Errorf("%s: WaitFirewall = %v, want ErrFirewallOpen", name, err)
		}
	}
}

func TestWaitFirewallWantsAnIPAddress(t *testing.T) {
	dial, dials := rounds([]error{dropped})
	err := WaitFirewall(context.Background(), "ingest.lan:8444", FirewallWait{Dial: dial})
	if err == nil || *dials != 0 {
		t.Fatalf("a host name: err %v after %d dials, want an error and no dial", err, *dials)
	}
}

// A probe that never answered proves nothing: the firewall may already apply, or something
// else drops the probe. Then the agent keeps the fixed delay, counted from its own start.
func TestWaitFirewallFallsBackToTheDelayWithoutProof(t *testing.T) {
	dial, _ := rounds([]error{dropped})
	start := time.Now()
	err := WaitFirewall(context.Background(), "10.50.0.2:8444", FirewallWait{Dial: dial, Interval: time.Millisecond, Limit: time.Second, Since: start, Settle: 150 * time.Millisecond})
	if err != nil {
		t.Fatalf("WaitFirewall = %v", err)
	}
	if waited := time.Since(start); waited < 150*time.Millisecond {
		t.Fatalf("waited %s, want at least the 150ms delay", waited)
	}
}

func TestWaitFirewallSaysWhetherTheProbeEverAnswered(t *testing.T) {
	dial, _ := rounds([]error{nil})
	if err := wait(dial, 20*time.Millisecond); err == nil || !contains(err.Error(), "still answers") {
		t.Fatalf("err = %v, want it to say the probe still answers", err)
	}
	dial, _ = rounds([]error{netDown})
	if err := wait(dial, 20*time.Millisecond); err == nil || !contains(err.Error(), "never reachable") {
		t.Fatalf("err = %v, want it to say the probe was never reachable", err)
	}
}

func TestWaitFirewallStopsWithItsContext(t *testing.T) {
	dial, _ := rounds([]error{nil})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := WaitFirewall(ctx, "10.50.0.2:8444", FirewallWait{Dial: dial, Interval: time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrFirewallOpen) {
		t.Fatalf("err = %v, want the context's error, not ErrFirewallOpen", err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
