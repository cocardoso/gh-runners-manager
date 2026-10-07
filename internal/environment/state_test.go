package environment

import (
	"testing"
	"time"
)

func TestTransitions(t *testing.T) {
	allowed := []struct{ from, to State }{
		{Pending, Provisioning},
		{Provisioning, Booting},
		{Booting, Connected},
		{Connected, Idle},
		{Idle, Running},
		{Idle, Completing},
		{Running, Completing},
		{Completing, Destroying},
		{Destroying, Destroyed},
		{Running, Failed},
		{Failed, Destroying},
		{Booting, Destroying},
	}
	for _, tc := range allowed {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s, %s) = false, want true", tc.from, tc.to)
		}
	}
	denied := []struct{ from, to State }{
		{Pending, Running},
		{Destroyed, Pending},
		{Destroyed, Destroying},
		{Failed, Running},
		{Running, Idle},
		{Completing, Running},
		{Destroying, Failed},
		{State("bogus"), Pending},
	}
	for _, tc := range denied {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s, %s) = true, want false", tc.from, tc.to)
		}
	}
}

func TestEveryTargetIsAValidState(t *testing.T) {
	for from, targets := range transitions {
		for _, to := range targets {
			if !to.Valid() {
				t.Errorf("%s -> %s targets an unknown state", from, to)
			}
		}
	}
}

func TestLive(t *testing.T) {
	if Destroyed.Live() {
		t.Error("Destroyed.Live() = true")
	}
	if !Failed.Live() || !Running.Live() || !Pending.Live() {
		t.Error("non-destroyed states must be live")
	}
	if State("bogus").Live() {
		t.Error("unknown state must not be live")
	}
}

func TestTimeoutsExpired(t *testing.T) {
	tt := DefaultTimeouts()
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if tt.Expired(Provisioning, since, since.Add(2*time.Minute)) {
		t.Error("exactly at the timeout must not be expired")
	}
	if !tt.Expired(Provisioning, since, since.Add(2*time.Minute+time.Nanosecond)) {
		t.Error("past the timeout must be expired")
	}
	if tt.Expired(Pending, since, since.Add(1000*time.Hour)) {
		t.Error("states without a timeout never expire")
	}
	want := map[State]time.Duration{
		Provisioning: 2 * time.Minute, Booting: 2 * time.Minute, Connected: 2 * time.Minute,
		Idle: 10 * time.Minute, Running: 6 * time.Hour, Completing: 5 * time.Minute,
	}
	for s, d := range want {
		if tt[s] != d {
			t.Errorf("DefaultTimeouts()[%s] = %v, want %v", s, tt[s], d)
		}
	}
}
