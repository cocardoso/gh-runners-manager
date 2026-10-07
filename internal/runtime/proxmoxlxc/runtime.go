// Package proxmoxlxc runs job environments as linked-clone LXC containers on Proxmox VE.
package proxmoxlxc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

const (
	// TagEnvironment marks every guest owned by ghrm.
	TagEnvironment = "ghrm-env"
	idTagPrefix    = "ghrmid-"
	cleanupTimeout = 2 * time.Minute
)

// ErrNoFreeVMID means every VMID in the configured range is taken.
var ErrNoFreeVMID = errors.New("proxmoxlxc: no free VMID in range")

// Config configures the runtime.
type Config struct {
	Node           string
	TemplateVMID   int
	Pool           string // optional resource pool for new guests
	VMIDStart      int
	VMIDEnd        int
	ThinPool       string // LV name of the thin pool, e.g. "data"
	FirewallSettle time.Duration
}

// Runtime implements runtime.Runtime on Proxmox LXC.
type Runtime struct {
	client *proxmox.Client
	cfg    Config
	sleep  func(context.Context, time.Duration) error
	mu     sync.Mutex // serializes Create so VMID allocation cannot race
}

// New returns a Runtime.
func New(client *proxmox.Client, cfg Config) *Runtime {
	return &Runtime{client: client, cfg: cfg, sleep: sleepContext}
}

// SetSleep replaces the firewall-settle sleep (tests only).
func (r *Runtime) SetSleep(fn func(context.Context, time.Duration) error) { r.sleep = fn }

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func refFor(vmid int) runtime.Ref { return runtime.Ref{ID: strconv.Itoa(vmid)} }

func vmidOf(ref runtime.Ref) (int, error) {
	vmid, err := strconv.Atoi(ref.ID)
	if err != nil || vmid <= 0 {
		return 0, fmt.Errorf("proxmoxlxc: invalid ref %q", ref.ID)
	}
	return vmid, nil
}

func idTag(id string) string { return idTagPrefix + id }

func environmentID(l proxmox.LXC) string {
	for _, t := range l.TagList() {
		if strings.HasPrefix(t, idTagPrefix) {
			return strings.TrimPrefix(t, idTagPrefix)
		}
	}
	return ""
}

func encodeEnv(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + env[k]
	}
	return strings.Join(pairs, "\x00")
}

// Create implements runtime.Runtime.
func (r *Runtime) Create(ctx context.Context, spec runtime.EnvironmentSpec) (runtime.Ref, error) {
	if err := spec.Validate(); err != nil {
		return runtime.Ref{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Ref{}, err
	}
	for _, g := range guests {
		if g.HasTag(idTag(spec.ID)) {
			return refFor(g.VMID), nil
		}
	}

	vmid, err := r.allocateVMID(ctx)
	if err != nil {
		return runtime.Ref{}, err
	}
	opts := proxmox.CloneOptions{
		Hostname:    spec.Hostname,
		Description: "Managed by gh-runners-manager. Environment " + spec.ID + ".",
		Pool:        r.cfg.Pool,
	}
	if err := r.client.CloneLXC(ctx, r.cfg.Node, r.cfg.TemplateVMID, vmid, opts); err != nil {
		return runtime.Ref{}, fmt.Errorf("clone template %d to %d: %w", r.cfg.TemplateVMID, vmid, err)
	}

	values := url.Values{
		"cores":  {strconv.Itoa(spec.Cores)},
		"memory": {strconv.Itoa(spec.MemoryMB)},
		"swap":   {"0"},
		"tags":   {TagEnvironment + ";" + idTag(spec.ID)},
	}
	if len(spec.Env) > 0 {
		values.Set("env", encodeEnv(spec.Env))
	}
	if err := r.client.SetLXCConfig(ctx, r.cfg.Node, vmid, values); err != nil {
		r.cleanup(ctx, vmid)
		return runtime.Ref{}, fmt.Errorf("configure %d: %w", vmid, err)
	}
	// The firewall rules of a new guest are applied on pve-firewall's next cycle (spec §10.3).
	if err := r.sleep(ctx, r.cfg.FirewallSettle); err != nil {
		r.cleanup(ctx, vmid)
		return runtime.Ref{}, fmt.Errorf("wait for firewall on %d: %w", vmid, err)
	}
	return refFor(vmid), nil
}

func (r *Runtime) allocateVMID(ctx context.Context) (int, error) {
	for vmid := r.cfg.VMIDStart; vmid <= r.cfg.VMIDEnd; vmid++ {
		free, err := r.client.VMIDAvailable(ctx, vmid)
		if err != nil {
			return 0, fmt.Errorf("check VMID %d: %w", vmid, err)
		}
		if free {
			return vmid, nil
		}
	}
	return 0, fmt.Errorf("%w %d-%d", ErrNoFreeVMID, r.cfg.VMIDStart, r.cfg.VMIDEnd)
}

// cleanup destroys a half-created guest even if ctx was cancelled.
func (r *Runtime) cleanup(ctx context.Context, vmid int) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_ = r.destroy(cctx, vmid)
}

// Start implements runtime.Runtime.
func (r *Runtime) Start(ctx context.Context, ref runtime.Ref) error {
	vmid, err := vmidOf(ref)
	if err != nil {
		return err
	}
	return mapNotFound(r.client.StartLXC(ctx, r.cfg.Node, vmid))
}

// Stop implements runtime.Runtime.
func (r *Runtime) Stop(ctx context.Context, ref runtime.Ref) error {
	vmid, err := vmidOf(ref)
	if err != nil {
		return err
	}
	return mapNotFound(r.client.StopLXC(ctx, r.cfg.Node, vmid))
}

// Destroy implements runtime.Runtime.
func (r *Runtime) Destroy(ctx context.Context, ref runtime.Ref) error {
	vmid, err := vmidOf(ref)
	if err != nil {
		return err
	}
	return r.destroy(ctx, vmid)
}

func (r *Runtime) destroy(ctx context.Context, vmid int) error {
	st, err := r.client.LXCCurrentStatus(ctx, r.cfg.Node, vmid)
	if errors.Is(err, proxmox.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Status == "running" {
		if err := r.client.StopLXC(ctx, r.cfg.Node, vmid); err != nil && !errors.Is(err, proxmox.ErrNotFound) {
			return fmt.Errorf("stop %d: %w", vmid, err)
		}
	}
	if err := r.client.DeleteLXC(ctx, r.cfg.Node, vmid); err != nil && !errors.Is(err, proxmox.ErrNotFound) {
		return fmt.Errorf("delete %d: %w", vmid, err)
	}
	return nil
}

// Status implements runtime.Runtime.
func (r *Runtime) Status(ctx context.Context, ref runtime.Ref) (runtime.Status, error) {
	vmid, err := vmidOf(ref)
	if err != nil {
		return runtime.Status{}, err
	}
	cur, err := r.client.LXCCurrentStatus(ctx, r.cfg.Node, vmid)
	if err != nil {
		return runtime.Status{}, mapNotFound(err)
	}
	st := runtime.Status{Ref: ref, Running: cur.Status == "running"}
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Status{}, err
	}
	for _, g := range guests {
		if g.VMID == vmid {
			st.EnvironmentID = environmentID(g)
		}
	}
	if st.Running {
		if ifaces, err := r.client.LXCInterfaces(ctx, r.cfg.Node, vmid); err == nil {
			for _, i := range ifaces {
				if i.Name == "eth0" && i.Inet != "" {
					st.IP, _, _ = strings.Cut(i.Inet, "/")
				}
			}
		}
	}
	return st, nil
}

// List implements runtime.Runtime.
func (r *Runtime) List(ctx context.Context) ([]runtime.Status, error) {
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return nil, err
	}
	var out []runtime.Status
	for _, g := range guests {
		if g.Template == 1 || !g.HasTag(TagEnvironment) {
			continue
		}
		out = append(out, runtime.Status{Ref: refFor(g.VMID), EnvironmentID: environmentID(g), Running: g.Status == "running"})
	}
	return out, nil
}

// Capacity implements runtime.Runtime.
func (r *Runtime) Capacity(ctx context.Context) (runtime.Capacity, error) {
	ns, err := r.client.NodeStatus(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Capacity{}, err
	}
	available := ns.Memory.Available
	if available == 0 {
		available = ns.Memory.Free
	}
	c := runtime.Capacity{HostMemoryTotalMB: int(ns.Memory.Total >> 20), HostMemoryAvailableMB: int(available >> 20)}

	pools, err := r.client.ThinPools(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Capacity{}, err
	}
	found := false
	for _, p := range pools {
		if p.LV != r.cfg.ThinPool {
			continue
		}
		found = true
		c.ThinPoolPercent = max(percent(p.Used, p.Size), percent(p.MetadataUsed, p.MetadataSize))
	}
	if !found {
		return runtime.Capacity{}, fmt.Errorf("proxmoxlxc: thin pool %q not found on node %s", r.cfg.ThinPool, r.cfg.Node)
	}

	envs, err := r.List(ctx)
	if err != nil {
		return runtime.Capacity{}, err
	}
	c.Environments = len(envs)
	return c, nil
}

func percent(used, size int64) float64 {
	if size <= 0 {
		return 0
	}
	return float64(used) * 100 / float64(size)
}

func mapNotFound(err error) error {
	if errors.Is(err, proxmox.ErrNotFound) {
		return fmt.Errorf("%w: %v", runtime.ErrNotFound, err)
	}
	return err
}
