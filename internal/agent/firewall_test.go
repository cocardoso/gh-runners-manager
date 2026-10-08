package agent

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// dialer answers with each result in turn, then repeats the last one.
func dialer(results ...error) (func(context.Context, string, string) (net.Conn, error), *int) {
	calls := 0
	return func(context.Context, string, string) (net.Conn, error) {
		r := results[min(calls, len(results)-1)]
		calls++
		if r == nil {
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		}
		return nil, r
	}, &calls
}

func refused() error {
	return &net.OpError{Op: "dial", Err: &osSyscallErr{syscall.ECONNREFUSED}}
}

type osSyscallErr struct{ errno syscall.Errno }

func (e *osSyscallErr) Error() string { return e.errno.Error() }
func (e *osSyscallErr) Unwrap() error { return e.errno }

func TestWaitFirewallStartsOnceTheProbeIsDropped(t *testing.T) {
	dial, calls := dialer(nil, refused(), &net.OpError{Op: "dial", Err: timeoutErr{}})
	err := WaitFirewall(context.Background(), "10.50.0.2:8444", FirewallWait{Dial: dial, Interval: time.Millisecond, Limit: time.Second})
	if err != nil {
		t.Fatalf("WaitFirewall = %v, want nil once the probe times out", err)
	}
	if *calls != 3 {
		t.Fatalf("probes = %d, want 3 (connected, refused, dropped)", *calls)
	}
}

func TestWaitFirewallTreatsAnUnreachableHostAsFiltered(t *testing.T) {
	dial, _ := dialer(nil, &net.OpError{Op: "dial", Err: &osSyscallErr{syscall.EHOSTUNREACH}})
	if err := WaitFirewall(context.Background(), "x:1", FirewallWait{Dial: dial, Interval: time.Millisecond, Limit: time.Second}); err != nil {
		t.Fatalf("WaitFirewall = %v, want nil (a REJECT rule answers host unreachable)", err)
	}
}

// A probe that never answered proves nothing: the firewall may already apply, or something
// else drops the probe. Then the agent keeps the fixed delay, counted from its own start.
func TestWaitFirewallFallsBackToTheDelayWithoutProof(t *testing.T) {
	dial, _ := dialer(&net.OpError{Op: "dial", Err: timeoutErr{}})
	start := time.Now()
	err := WaitFirewall(context.Background(), "x:1", FirewallWait{Dial: dial, Interval: time.Millisecond, Limit: time.Second, Since: start, Settle: 150 * time.Millisecond})
	if err != nil {
		t.Fatalf("WaitFirewall = %v", err)
	}
	if waited := time.Since(start); waited < 150*time.Millisecond {
		t.Fatalf("waited %s, want at least the 150ms delay", waited)
	}
}

func TestWaitFirewallFailsWhileTheProbeStillAnswers(t *testing.T) {
	dial, _ := dialer(nil)
	err := WaitFirewall(context.Background(), "x:1", FirewallWait{Dial: dial, Interval: time.Millisecond, Limit: 20 * time.Millisecond})
	if !errors.Is(err, ErrFirewallOpen) {
		t.Fatalf("WaitFirewall = %v, want ErrFirewallOpen", err)
	}
	// A network that is not up yet is no proof either.
	dial, _ = dialer(&net.OpError{Op: "dial", Err: &osSyscallErr{syscall.ENETUNREACH}})
	if err := WaitFirewall(context.Background(), "x:1", FirewallWait{Dial: dial, Interval: time.Millisecond, Limit: 20 * time.Millisecond}); !errors.Is(err, ErrFirewallOpen) {
		t.Fatalf("WaitFirewall on an unreachable network = %v, want ErrFirewallOpen", err)
	}
}
