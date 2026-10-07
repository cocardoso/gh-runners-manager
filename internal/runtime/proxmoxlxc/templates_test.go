package proxmoxlxc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox/proxmoxtest"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

var _ runtime.Templates = (*Runtime)(nil)

func templateHarness(t *testing.T) *harness {
	h := newHarness(t, 900, 909)
	h.rt.cfg.Templates = TemplateConfig{VMIDStart: 950, VMIDEnd: 952, Storage: "local", RootFSGB: 16, Nameserver: "1.1.1.1", Bridge: "jobnet", FirewallGroup: "gh-runner"}
	return h
}

func archive() runtime.TemplateSpec {
	b := []byte("zstd-archive")
	return runtime.TemplateSpec{ID: "tpl01", Archive: bytes.NewReader(b), Size: int64(len(b)), SHA256: "c2f3a8ac5cd4a3f2f48b5c1bf2c06b2fdf1a5a2f9b2a4d0b1d0e8b9e6c3a8f10"}
}

func withSHA(s runtime.TemplateSpec) runtime.TemplateSpec {
	b := []byte("zstd-archive")
	s.SHA256 = sha256hex(b)
	return s
}

func TestCreateTemplateUploadsCreatesAndConverts(t *testing.T) {
	h := templateHarness(t)
	ref, err := h.rt.CreateTemplate(context.Background(), withSHA(archive()))
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "950/tpl01" {
		t.Fatalf("ref = %v", ref)
	}
	g, ok := h.srv.Guest(950)
	if !ok || !g.Template || g.Config["ostemplate"] != "local:vztmpl/ghrm-tpl01.tar.zst" || g.Config["pool"] != "ghrm" {
		t.Fatalf("template guest = %+v", g)
	}
	if !strings.Contains(g.Tags, "ghrm-template") || !strings.Contains(g.Tags, "ghrmtpl-tpl01") {
		t.Fatalf("tags = %q", g.Tags)
	}
	if fw := h.srv.Firewall(950); !fw.Enabled || len(fw.Groups) != 1 || fw.Groups[0] != "gh-runner" {
		t.Fatalf("firewall = %+v", fw)
	}
	if _, ok := h.srv.Volume("local:vztmpl/ghrm-tpl01.tar.zst"); !ok {
		t.Fatal("archive volume missing")
	}
}

func TestCreateTemplateCleansUpOnFailure(t *testing.T) {
	h := templateHarness(t)
	h.srv.FailCreate = true
	if _, err := h.rt.CreateTemplate(context.Background(), withSHA(archive())); err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("err = %v, want a create-stage error", err)
	}
	if _, ok := h.srv.Guest(950); ok {
		t.Fatal("half-created template left behind")
	}
	if vols := h.srv.Volumes(); len(vols) != 0 {
		t.Fatalf("archive left behind: %+v", vols)
	}
	// A bad checksum fails at upload and leaves nothing either.
	bad := archive()
	if _, err := h.rt.CreateTemplate(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "upload") {
		t.Fatalf("err = %v, want an upload-stage error", err)
	}
}

func TestDeleteTemplateOwnershipAndUse(t *testing.T) {
	h := templateHarness(t)
	ctx := context.Background()
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 951, Type: "lxc", Template: true, Tags: "ghrm-template", Config: map[string]string{"pool": "ghrm"}})
	if err := h.rt.DeleteTemplate(ctx, runtime.TemplateRef{ID: "951/other"}); !errors.Is(err, ErrNotOwned) {
		t.Fatalf("foreign template: err = %v, want ErrNotOwned", err)
	}
	ref, err := h.rt.CreateTemplate(ctx, withSHA(archive()))
	if err != nil {
		t.Fatal(err)
	}
	s := spec("job1")
	s.Template = "950"
	envRef, err := h.rt.Create(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if used, err := h.rt.TemplateInUse(ctx, ref); err != nil || !used {
		t.Fatalf("in use = %v, %v; want true while a clone exists", used, err)
	}
	if err := h.rt.DeleteTemplate(ctx, ref); !errors.Is(err, runtime.ErrTemplateInUse) {
		t.Fatalf("delete in use: %v", err)
	}
	if err := h.rt.Destroy(ctx, envRef); err != nil {
		t.Fatal(err)
	}
	if err := h.rt.DeleteTemplate(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.srv.Guest(950); ok {
		t.Fatal("template guest not deleted")
	}
	if vols := h.srv.Volumes(); len(vols) != 0 {
		t.Fatalf("archive not deleted: %+v", vols)
	}
}

func TestCreateClonesTheChosenTemplateAndGrowsTheDisk(t *testing.T) {
	h := templateHarness(t)
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 951, Type: "lxc", Template: true, Tags: "ghrm-template;ghrmtpl-x", Config: map[string]string{"pool": "ghrm"}})
	s := spec("bld")
	s.Template = "951"
	s.DiskGB = 40
	ref, err := h.rt.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := h.srv.Guest(900)
	if !strings.Contains(g.Config["rootfs"], "base-951-") || g.Config["rootfs_size"] != "40G" || ref.ID != "900/bld" {
		t.Fatalf("clone = %+v", g.Config)
	}
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
