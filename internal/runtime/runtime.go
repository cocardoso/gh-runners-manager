// Package runtime abstracts the infrastructure that runs job environments (spec §4.2).
package runtime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrNotFound is returned by Status (and Stop) when the environment does not exist.
	ErrNotFound = errors.New("runtime: environment not found")
	// ErrInvalidSpec is wrapped by EnvironmentSpec.Validate errors.
	ErrInvalidSpec = errors.New("runtime: invalid environment spec")
)

// EnvironmentSpec describes one environment to create.
type EnvironmentSpec struct {
	ID       string // ghrm environment ID: lowercase ULID
	Hostname string // DNS label
	Cores    int
	MemoryMB int
	Env      map[string]string // variables visible to the guest's init process
}

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9]{1,40}$`)
	hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envKeyPattern   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// Validate rejects specs that a runtime could not apply safely.
func (s EnvironmentSpec) Validate() error {
	var problems []string
	if !idPattern.MatchString(s.ID) {
		problems = append(problems, fmt.Sprintf("id %q must be 1-40 lowercase letters or digits", s.ID))
	}
	if !hostnamePattern.MatchString(s.Hostname) {
		problems = append(problems, fmt.Sprintf("hostname %q is not a valid DNS label", s.Hostname))
	}
	if s.Cores < 1 {
		problems = append(problems, "cores must be at least 1")
	}
	if s.MemoryMB < 256 {
		problems = append(problems, "memory must be at least 256 MB")
	}
	for k, v := range s.Env {
		if !envKeyPattern.MatchString(k) {
			problems = append(problems, fmt.Sprintf("env key %q must match %s", k, envKeyPattern))
		}
		if strings.ContainsAny(v, "\x00\n\r") {
			problems = append(problems, fmt.Sprintf("env %s must not contain NUL or line breaks", k))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidSpec, strings.Join(problems, "; "))
	}
	return nil
}

// Ref identifies an environment inside a runtime, for example a Proxmox VMID.
type Ref struct{ ID string }

func (r Ref) String() string { return r.ID }

// Status is the runtime's view of one environment.
type Status struct {
	Ref           Ref
	EnvironmentID string
	Running       bool
	IP            string
}

// Capacity is the runtime's view of host resources.
type Capacity struct {
	HostMemoryTotalMB     int
	HostMemoryAvailableMB int
	ThinPoolPercent       float64
	Environments          int
}

// Runtime creates and destroys job environments.
type Runtime interface {
	// Create provisions a stopped environment that is ready to start. It is idempotent per spec.ID.
	Create(ctx context.Context, spec EnvironmentSpec) (Ref, error)
	Start(ctx context.Context, ref Ref) error
	Stop(ctx context.Context, ref Ref) error
	// Destroy removes the environment. Destroying a missing environment is not an error.
	Destroy(ctx context.Context, ref Ref) error
	// List returns every environment owned by ghrm in this runtime.
	List(ctx context.Context) ([]Status, error)
	// Status returns ErrNotFound when the environment does not exist.
	Status(ctx context.Context, ref Ref) (Status, error)
	Capacity(ctx context.Context) (Capacity, error)
}
