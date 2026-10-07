// Package runtime abstracts the infrastructure that runs job environments (spec §4.2).
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	// ErrNotFound is returned by Status (and Stop) when the environment does not exist.
	ErrNotFound = errors.New("runtime: environment not found")
	// ErrInvalidSpec is wrapped by EnvironmentSpec.Validate errors.
	ErrInvalidSpec = errors.New("runtime: invalid environment spec")
	// ErrTemplateInUse means an environment still depends on the template.
	ErrTemplateInUse = errors.New("runtime: template is in use")
)

// EnvironmentSpec describes one environment to create.
type EnvironmentSpec struct {
	ID       string // ghrm environment ID: lowercase ULID
	Hostname string // DNS label
	Cores    int
	MemoryMB int
	Env      map[string]string // variables visible to the guest's init process
	// Template is the runtime's reference of the template to clone ("" = the configured bootstrap template).
	Template string
	// DiskGB grows the root disk after cloning (0 keeps the template's size).
	DiskGB int
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
	if s.DiskGB < 0 {
		problems = append(problems, "disk size must not be negative")
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

// TemplateSpec is a root filesystem archive to turn into a template.
type TemplateSpec struct {
	ID      string    // template version ID (lowercase letters and digits)
	Archive io.Reader // .tar.zst root filesystem
	Size    int64
	SHA256  string // hex SHA-256 of the archive, verified by the hypervisor
}

// TemplateRef identifies a template in the runtime.
type TemplateRef struct{ ID string }

func (r TemplateRef) String() string { return r.ID }

// Templates is implemented by runtimes that can build templates (spec §8.3).
type Templates interface {
	// CreateTemplate stores the archive and creates a template from it. A failure leaves nothing behind.
	CreateTemplate(ctx context.Context, spec TemplateSpec) (TemplateRef, error)
	// DeleteTemplate removes a template and its archive. It returns ErrTemplateInUse while a clone exists.
	DeleteTemplate(ctx context.Context, ref TemplateRef) error
	// TemplateInUse reports whether an environment was cloned from the template and still exists.
	TemplateInUse(ctx context.Context, ref TemplateRef) (bool, error)
	// TemplateEnvironmentRef returns the reference environments use to clone the template (EnvironmentSpec.Template).
	TemplateEnvironmentRef(ref TemplateRef) string
}
