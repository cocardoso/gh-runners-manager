// Package environment defines the lifecycle of a job environment.
package environment

import "time"

// State is a lifecycle state of a job environment (spec §5).
type State string

const (
	Pending      State = "pending"
	Provisioning State = "provisioning"
	Booting      State = "booting"
	Connected    State = "connected"
	Idle         State = "idle"
	Running      State = "running"
	Completing   State = "completing"
	Destroying   State = "destroying"
	Destroyed    State = "destroyed"
	Failed       State = "failed"
)

var transitions = map[State][]State{
	Pending:      {Provisioning, Failed, Destroying},
	Provisioning: {Booting, Failed, Destroying},
	Booting:      {Connected, Failed, Destroying},
	Connected:    {Idle, Failed, Destroying},
	Idle:         {Running, Completing, Failed, Destroying},
	Running:      {Completing, Failed, Destroying},
	Completing:   {Destroying, Failed},
	Failed:       {Destroying},
	Destroying:   {Destroyed},
	Destroyed:    nil,
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// Live reports whether an environment in state s still exists or may exist.
func (s State) Live() bool { return s.Valid() && s != Destroyed }

// CanTransition reports whether an environment may move from one state to another.
func CanTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Timeouts bounds how long an environment may stay in a state. States without an entry never time out.
type Timeouts map[State]time.Duration

// DefaultTimeouts returns the timeouts from spec §5.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Pending:      2 * time.Minute,
		Provisioning: 2 * time.Minute,
		Booting:      2 * time.Minute,
		Connected:    2 * time.Minute,
		Idle:         10 * time.Minute,
		Running:      6 * time.Hour,
		Completing:   5 * time.Minute,
	}
}

// Expired reports whether an environment that entered state at since has outlived its timeout at now.
func (t Timeouts) Expired(state State, since, now time.Time) bool {
	d, ok := t[state]
	return ok && now.Sub(since) > d
}
