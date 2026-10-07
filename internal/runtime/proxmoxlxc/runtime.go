// Package proxmoxlxc runs job environments as linked-clone LXC containers on Proxmox VE.
package proxmoxlxc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	destroyRetries = 3
)

var (
	// ErrNoFreeVMID means every VMID in the configured range is taken.
	ErrNoFreeVMID = errors.New("proxmoxlxc: no free VMID in range")
	// ErrNotOwned means a ref points at a guest that ghrm does not manage.
	ErrNotOwned = errors.New("proxmoxlxc: guest is not a ghrm environment")
)

// Config configures the runtime.
type Config struct {
	Node           string
	TemplateVMID   int
	Pool           string // optional resource pool for new guests
	VMIDStart      int
	VMIDEnd        int
	ThinPool       string // LV name of the thin pool, e.g. "data"
	Storage        string // Proxmox storage ID of the thin pool, e.g. "local-lvm"
	FirewallSettle time.Duration
}

// Runtime implements runtime.Runtime on Proxmox LXC.
//
// Refs have the form "<vmid>/<environment id>". Every operation on an existing
// environment first looks the guest up in the node's LXC list, which a
// pool-scoped token sees only for guests in its pool. A guest that is absent,
// or whose ghrmid tag names another environment (the VMID was reused), is
// treated as gone; per-guest endpoints are never asked about guests that may
// have left the pool, because Proxmox answers those with 403.
type Runtime struct {
	client *proxmox.Client
	cfg    Config
	sleep  func(context.Context, time.Duration) error

	allocMu sync.Mutex // held from VMID allocation until the clone exists
	idMu    sync.Mutex
	idLocks map[string]*sync.Mutex // per environment ID, for Create idempotency
}

// New returns a Runtime.
func New(client *proxmox.Client, cfg Config) *Runtime {
	return &Runtime{client: client, cfg: cfg, sleep: sleepContext, idLocks: map[string]*sync.Mutex{}}
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

func refFor(vmid int, envID string) runtime.Ref {
	return runtime.Ref{ID: strconv.Itoa(vmid) + "/" + envID}
}

func (r *Runtime) parseRef(ref runtime.Ref) (int, string, error) {
	vmidStr, envID, ok := strings.Cut(ref.ID, "/")
	vmid, err := strconv.Atoi(vmidStr)
	if !ok || err != nil || vmid <= 0 || envID == "" {
		return 0, "", fmt.Errorf("proxmoxlxc: invalid ref %q", ref.ID)
	}
	if vmid < r.cfg.VMIDStart || vmid > r.cfg.VMIDEnd {
		return 0, "", fmt.Errorf("%w: VMID %d is outside %d-%d", ErrNotOwned, vmid, r.cfg.VMIDStart, r.cfg.VMIDEnd)
	}
	return vmid, envID, nil
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

func (r *Runtime) lockID(id string) func() {
	r.idMu.Lock()
	m, ok := r.idLocks[id]
	if !ok {
		m = &sync.Mutex{}
		r.idLocks[id] = m
	}
	r.idMu.Unlock()
	m.Lock()
	return m.Unlock
}

// lookup returns the guest for vmid if it exists, is visible and belongs to envID.
func (r *Runtime) lookup(ctx context.Context, vmid int, envID string) (*proxmox.LXC, error) {
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return nil, err
	}
	for _, g := range guests {
		if g.VMID == vmid && g.Template != 1 && g.HasTag(idTag(envID)) {
			return &g, nil
		}
	}
	return nil, nil
}

// Create implements runtime.Runtime.
func (r *Runtime) Create(ctx context.Context, spec runtime.EnvironmentSpec) (runtime.Ref, error) {
	if err := spec.Validate(); err != nil {
		return runtime.Ref{}, err
	}
	unlock := r.lockID(spec.ID)
	defer unlock()

	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Ref{}, err
	}
	known := make(map[int]bool, len(guests))
	for _, g := range guests {
		if g.HasTag(idTag(spec.ID)) {
			return refFor(g.VMID, spec.ID), nil
		}
		known[g.VMID] = true
	}

	vmid, err := r.cloneNew(ctx, spec, known)
	if err != nil {
		return runtime.Ref{}, err
	}

	values := url.Values{
		"cores":  {strconv.Itoa(spec.Cores)},
		"memory": {strconv.Itoa(spec.MemoryMB)},
		"swap":   {"0"},
	}
	if len(spec.Env) > 0 {
		values.Set("env", encodeEnv(spec.Env))
	}
	if err := r.client.SetLXCConfig(ctx, r.cfg.Node, vmid, values); err != nil {
		if strings.Contains(err.Error(), `"env"`) && strings.Contains(err.Error(), "not defined in schema") {
			err = fmt.Errorf("the LXC env option needs Proxmox VE 9.1 or later: %w", err)
		}
		return runtime.Ref{}, r.abandon(ctx, vmid, fmt.Errorf("configure %d: %w", vmid, err))
	}
	// The firewall rules of a new guest are applied on pve-firewall's next cycle (spec §10.3).
	if err := r.sleep(ctx, r.cfg.FirewallSettle); err != nil {
		return runtime.Ref{}, r.abandon(ctx, vmid, fmt.Errorf("wait for firewall on %d: %w", vmid, err))
	}
	return refFor(vmid, spec.ID), nil
}

// cloneNew allocates a VMID, clones the template into it and tags the clone at once,
// so that a half-created guest is always visible to List. The allocation lock is
// released as soon as the clone exists: from then on its VMID is taken.
func (r *Runtime) cloneNew(ctx context.Context, spec runtime.EnvironmentSpec, known map[int]bool) (int, error) {
	r.allocMu.Lock()
	vmid, err := r.allocateVMID(ctx, known)
	if err != nil {
		r.allocMu.Unlock()
		return 0, err
	}
	opts := proxmox.CloneOptions{
		Hostname:    spec.Hostname,
		Description: "Managed by gh-runners-manager. Environment " + spec.ID + ".",
		Pool:        r.cfg.Pool,
	}
	err = r.client.CloneLXC(ctx, r.cfg.Node, r.cfg.TemplateVMID, vmid, opts)
	r.allocMu.Unlock()
	if err != nil {
		err = fmt.Errorf("clone template %d to %d: %w", r.cfg.TemplateVMID, vmid, err)
		var taskErr *proxmox.TaskError
		if errors.As(err, &taskErr) {
			// The clone task started: wait for it to end, then remove whatever it produced.
			return 0, r.abandonTask(ctx, vmid, taskErr.UPID, err)
		}
		return 0, err // rejected before any task started: nothing of ours exists
	}
	tags := url.Values{"tags": {TagEnvironment + ";" + idTag(spec.ID)}}
	if err := r.client.SetLXCConfig(ctx, r.cfg.Node, vmid, tags); err != nil {
		return 0, r.abandon(ctx, vmid, fmt.Errorf("tag %d: %w", vmid, err))
	}
	return vmid, nil
}

// allocateVMID returns the lowest free VMID in range. VMIDs already seen in the
// LXC list are skipped without asking; the rest are checked against the whole
// cluster, including guests the token cannot see.
func (r *Runtime) allocateVMID(ctx context.Context, known map[int]bool) (int, error) {
	for vmid := r.cfg.VMIDStart; vmid <= r.cfg.VMIDEnd; vmid++ {
		if known[vmid] {
			continue
		}
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

// abandon destroys a guest this Create made, even if ctx was cancelled, and
// reports a cleanup failure together with the original error.
func (r *Runtime) abandon(ctx context.Context, vmid int, cause error) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := r.destroyVMID(cctx, vmid, ""); err != nil {
		return errors.Join(cause, fmt.Errorf("cleanup of %d failed, remove it manually: %w", vmid, err))
	}
	return cause
}

// abandonTask waits for a started clone task to finish, then destroys its result.
func (r *Runtime) abandonTask(ctx context.Context, vmid int, upid string, cause error) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_ = r.client.WaitTask(cctx, r.cfg.Node, upid) // a failed clone leaves nothing or a partial guest
	if err := r.destroyVMID(cctx, vmid, ""); err != nil {
		return errors.Join(cause, fmt.Errorf("cleanup of %d failed, remove it manually: %w", vmid, err))
	}
	return cause
}

// Start implements runtime.Runtime.
func (r *Runtime) Start(ctx context.Context, ref runtime.Ref) error {
	vmid, envID, err := r.parseRef(ref)
	if err != nil {
		return err
	}
	g, err := r.lookup(ctx, vmid, envID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("%w: %s", runtime.ErrNotFound, ref)
	}
	return r.client.StartLXC(ctx, r.cfg.Node, vmid)
}

// Stop implements runtime.Runtime. Stopping a stopped environment succeeds.
func (r *Runtime) Stop(ctx context.Context, ref runtime.Ref) error {
	vmid, envID, err := r.parseRef(ref)
	if err != nil {
		return err
	}
	g, err := r.lookup(ctx, vmid, envID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("%w: %s", runtime.ErrNotFound, ref)
	}
	cur, err := r.client.LXCCurrentStatus(ctx, r.cfg.Node, vmid)
	if err != nil {
		return err
	}
	if cur.Status != "running" {
		return nil
	}
	return r.client.StopLXC(ctx, r.cfg.Node, vmid)
}

// Destroy implements runtime.Runtime.
func (r *Runtime) Destroy(ctx context.Context, ref runtime.Ref) error {
	vmid, envID, err := r.parseRef(ref)
	if err != nil {
		return err
	}
	return r.destroyVMID(ctx, vmid, envID)
}

// destroyVMID stops and deletes vmid. With envID set, a guest that is not visible
// or belongs to another environment is left alone and reported as gone. With envID
// empty (cleanup of a guest this process just cloned), the ownership check is skipped.
// Each attempt re-reads the guest, so a concurrent destroy or a guest stopping on
// its own does not turn into a failure.
func (r *Runtime) destroyVMID(ctx context.Context, vmid int, envID string) error {
	var lastErr error
	for range destroyRetries {
		st, err := r.guestState(ctx, vmid, envID)
		if err != nil {
			return err
		}
		if st == "" {
			return nil
		}
		if st == "running" {
			if err := r.client.StopLXC(ctx, r.cfg.Node, vmid); err != nil {
				lastErr = fmt.Errorf("stop %d: %w", vmid, err)
				continue
			}
		}
		if err := r.client.DeleteLXC(ctx, r.cfg.Node, vmid); err != nil {
			if errors.Is(err, proxmox.ErrNotFound) {
				return nil
			}
			lastErr = fmt.Errorf("delete %d: %w", vmid, err)
			continue
		}
		return nil
	}
	return lastErr
}

// guestState returns the guest's live status, or "" when it is gone (or not ours).
func (r *Runtime) guestState(ctx context.Context, vmid int, envID string) (string, error) {
	if envID != "" {
		g, err := r.lookup(ctx, vmid, envID)
		if err != nil || g == nil {
			return "", err
		}
	}
	// Live state: the list's status is cached and can lag behind a start or stop.
	cur, err := r.client.LXCCurrentStatus(ctx, r.cfg.Node, vmid)
	if errors.Is(err, proxmox.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return cur.Status, nil
}

// Status implements runtime.Runtime.
func (r *Runtime) Status(ctx context.Context, ref runtime.Ref) (runtime.Status, error) {
	vmid, envID, err := r.parseRef(ref)
	if err != nil {
		return runtime.Status{}, err
	}
	g, err := r.lookup(ctx, vmid, envID)
	if err != nil {
		return runtime.Status{}, err
	}
	if g == nil {
		return runtime.Status{}, fmt.Errorf("%w: %s", runtime.ErrNotFound, ref)
	}
	// The list's status comes from pvestatd's cache and lags behind a start or stop;
	// once the guest is known to be ours, ask for its live state.
	cur, err := r.client.LXCCurrentStatus(ctx, r.cfg.Node, vmid)
	if errors.Is(err, proxmox.ErrNotFound) {
		return runtime.Status{}, fmt.Errorf("%w: %s", runtime.ErrNotFound, ref)
	}
	if err != nil {
		return runtime.Status{}, err
	}
	st := runtime.Status{Ref: ref, EnvironmentID: envID, Running: cur.Status == "running"}
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
		id := environmentID(g)
		out = append(out, runtime.Status{Ref: refFor(g.VMID, id), EnvironmentID: id, Running: g.Status == "running"})
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

	pct, err := r.diskPercent(ctx)
	if err != nil {
		return runtime.Capacity{}, err
	}
	c.ThinPoolPercent = pct

	envs, err := r.List(ctx)
	if err != nil {
		return runtime.Capacity{}, err
	}
	c.Environments = len(envs)
	return c, nil
}

// diskPercent returns the larger of the thin pool's data and metadata usage. A
// pool-scoped token cannot read /disks/lvmthin (it needs Sys.Audit on "/"), so on
// 403 it falls back to the storage status, which reports data usage only.
func (r *Runtime) diskPercent(ctx context.Context) (float64, error) {
	pools, err := r.client.ThinPools(ctx, r.cfg.Node)
	var apiErr *proxmox.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		st, err := r.client.StorageStatus(ctx, r.cfg.Node, r.cfg.Storage)
		if err != nil {
			return 0, fmt.Errorf("storage %s status: %w", r.cfg.Storage, err)
		}
		return percent(st.Used, st.Total), nil
	}
	if err != nil {
		return 0, err
	}
	for _, p := range pools {
		if p.LV == r.cfg.ThinPool {
			return max(percent(p.Used, p.Size), percent(p.MetadataUsed, p.MetadataSize)), nil
		}
	}
	return 0, fmt.Errorf("proxmoxlxc: thin pool %q not found on node %s", r.cfg.ThinPool, r.cfg.Node)
}

func percent(used, size int64) float64 {
	if size <= 0 {
		return 0
	}
	return float64(used) * 100 / float64(size)
}
