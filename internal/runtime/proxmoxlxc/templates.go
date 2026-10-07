package proxmoxlxc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

const (
	// TagTemplate marks every template built by ghrm.
	TagTemplate       = "ghrm-template"
	templateTagPrefix = "ghrmtpl-"
)

func archiveName(id string) string { return "ghrm-" + id + ".tar.zst" }

func (r *Runtime) parseTemplateRef(ref runtime.TemplateRef) (int, string, error) {
	vmidStr, id, ok := strings.Cut(ref.ID, "/")
	vmid, err := strconv.Atoi(vmidStr)
	if !ok || err != nil || id == "" {
		return 0, "", fmt.Errorf("proxmoxlxc: invalid template ref %q", ref.ID)
	}
	t := r.cfg.Templates
	if vmid < t.VMIDStart || vmid > t.VMIDEnd {
		return 0, "", fmt.Errorf("%w: template VMID %d is outside %d-%d", ErrNotOwned, vmid, t.VMIDStart, t.VMIDEnd)
	}
	return vmid, id, nil
}

// TemplateEnvironmentRef implements runtime.Templates: environments clone the template's VMID.
func (r *Runtime) TemplateEnvironmentRef(ref runtime.TemplateRef) string {
	vmid, _, _ := strings.Cut(ref.ID, "/")
	return vmid
}

// CreateTemplate implements runtime.Templates: upload the archive, create the container with
// the job settings, attach the security group and convert it. Any failure removes what was made.
func (r *Runtime) CreateTemplate(ctx context.Context, spec runtime.TemplateSpec) (runtime.TemplateRef, error) {
	if !idPattern(spec.ID) || spec.Size <= 0 || len(spec.SHA256) != 64 {
		return runtime.TemplateRef{}, fmt.Errorf("%w: template id, size and SHA-256 are required", runtime.ErrInvalidSpec)
	}
	t := r.cfg.Templates
	volid, err := r.client.UploadTemplate(ctx, r.cfg.Node, t.Storage, archiveName(spec.ID), spec.Archive, spec.Size, spec.SHA256)
	if err != nil {
		return runtime.TemplateRef{}, r.dropVolume(ctx, t.Storage+":vztmpl/"+archiveName(spec.ID), fmt.Errorf("upload template archive: %w", err))
	}

	r.allocMu.Lock()
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		r.allocMu.Unlock()
		return runtime.TemplateRef{}, r.dropVolume(ctx, volid, fmt.Errorf("list guests: %w", err))
	}
	known := map[int]bool{}
	for _, g := range guests {
		known[g.VMID] = true
	}
	vmid, err := r.allocateIn(ctx, known, t.VMIDStart, t.VMIDEnd)
	if err != nil {
		r.allocMu.Unlock()
		return runtime.TemplateRef{}, r.dropVolume(ctx, volid, err)
	}
	// A template's own size does not matter: every clone gets the cores and memory of its scale set.
	cores, mem := 2, 2048
	err = r.client.CreateLXC(ctx, r.cfg.Node, proxmox.CreateLXCOptions{
		VMID: vmid, OSTemplate: volid, Hostname: "ghrm-template", Pool: r.cfg.Pool, Storage: r.cfg.Storage, RootFSGB: t.RootFSGB,
		Cores: cores, MemoryMB: mem, Nameserver: t.Nameserver, Bridge: t.Bridge, Tags: []string{TagTemplate, templateTagPrefix + spec.ID},
	})
	r.allocMu.Unlock()
	if err != nil {
		err = fmt.Errorf("create template container %d: %w", vmid, err)
		var taskErr *proxmox.TaskError
		if !errors.As(err, &taskErr) {
			// Rejected before any task started: the guest at vmid (if any) is not ours.
			return runtime.TemplateRef{}, r.dropVolume(ctx, volid, err)
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		_ = r.client.WaitTask(cctx, r.cfg.Node, taskErr.UPID)
		cancel()
		return runtime.TemplateRef{}, r.dropTemplate(ctx, vmid, volid, err)
	}
	if t.FirewallGroup != "" {
		if err := r.client.EnableFirewallGroup(ctx, r.cfg.Node, vmid, t.FirewallGroup); err != nil {
			return runtime.TemplateRef{}, r.dropTemplate(ctx, vmid, volid, fmt.Errorf("attach firewall group to %d: %w", vmid, err))
		}
	}
	if err := r.client.ConvertToTemplate(ctx, r.cfg.Node, vmid); err != nil {
		return runtime.TemplateRef{}, r.dropTemplate(ctx, vmid, volid, fmt.Errorf("convert %d to a template: %w", vmid, err))
	}
	return runtime.TemplateRef{ID: strconv.Itoa(vmid) + "/" + spec.ID}, nil
}

// dropVolume deletes an uploaded archive after a failure, if it exists.
func (r *Runtime) dropVolume(ctx context.Context, volid string, cause error) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	vols, err := r.client.StorageContent(cctx, r.cfg.Node, r.cfg.Templates.Storage, "vztmpl")
	if err != nil {
		return errors.Join(cause, fmt.Errorf("cleanup: list %s: %w", r.cfg.Templates.Storage, err))
	}
	for _, v := range vols {
		if v.VolID == volid {
			if err := r.client.DeleteVolume(cctx, r.cfg.Node, r.cfg.Templates.Storage, volid); err != nil {
				return errors.Join(cause, fmt.Errorf("cleanup of %s failed, remove it manually: %w", volid, err))
			}
		}
	}
	return cause
}

// dropTemplate deletes a half-created template container and its archive.
func (r *Runtime) dropTemplate(ctx context.Context, vmid int, volid string, cause error) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := r.client.DeleteLXC(cctx, r.cfg.Node, vmid); err != nil && !errors.Is(err, proxmox.ErrNotFound) {
		cause = errors.Join(cause, fmt.Errorf("cleanup of template %d failed, remove it manually: %w", vmid, err))
	}
	return r.dropVolume(ctx, volid, cause)
}

// TemplateInUse implements runtime.Templates: a linked clone's root disk names its base.
func (r *Runtime) TemplateInUse(ctx context.Context, ref runtime.TemplateRef) (bool, error) {
	vmid, _, err := r.parseTemplateRef(ref)
	if err != nil {
		return false, err
	}
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return false, err
	}
	base := fmt.Sprintf("base-%d-disk", vmid)
	for _, g := range guests {
		if g.Template == 1 || !g.HasTag(TagEnvironment) {
			continue
		}
		cfg, err := r.client.LXCConfig(ctx, r.cfg.Node, g.VMID)
		if errors.Is(err, proxmox.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if rootfs, _ := cfg["rootfs"].(string); strings.Contains(rootfs, base) {
			return true, nil
		}
	}
	return false, nil
}

// DeleteTemplate implements runtime.Templates. It only deletes guests tagged as this template.
func (r *Runtime) DeleteTemplate(ctx context.Context, ref runtime.TemplateRef) error {
	vmid, id, err := r.parseTemplateRef(ref)
	if err != nil {
		return err
	}
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return err
	}
	volid := r.cfg.Templates.Storage + ":vztmpl/" + archiveName(id)
	for _, g := range guests {
		if g.VMID != vmid {
			continue
		}
		if !g.HasTag(templateTagPrefix + id) {
			return fmt.Errorf("%w: guest %d is not template %s", ErrNotOwned, vmid, id)
		}
		used, err := r.TemplateInUse(ctx, ref)
		if err != nil {
			return err
		}
		if used {
			return fmt.Errorf("%w: %s", runtime.ErrTemplateInUse, ref)
		}
		if err := r.client.DeleteLXC(ctx, r.cfg.Node, vmid); err != nil && !errors.Is(err, proxmox.ErrNotFound) {
			return fmt.Errorf("delete template %d: %w", vmid, err)
		}
	}
	if err := r.dropVolume(ctx, volid, nil); err != nil {
		return err
	}
	return nil
}

// CleanupTemplate implements runtime.Templates: it removes the guest tagged as this version
// (in the template range, unless in use) and the version's archive.
func (r *Runtime) CleanupTemplate(ctx context.Context, id string) error {
	if !idPattern(id) {
		return fmt.Errorf("%w: template id %q", runtime.ErrInvalidSpec, id)
	}
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return err
	}
	t := r.cfg.Templates
	for _, g := range guests {
		if g.VMID < t.VMIDStart || g.VMID > t.VMIDEnd || !g.HasTag(templateTagPrefix+id) {
			continue
		}
		if err := r.DeleteTemplate(ctx, runtime.TemplateRef{ID: strconv.Itoa(g.VMID) + "/" + id}); err != nil {
			return err
		}
	}
	return r.dropVolume(ctx, t.Storage+":vztmpl/"+archiveName(id), nil)
}

func idPattern(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
