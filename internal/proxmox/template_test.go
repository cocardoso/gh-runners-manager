package proxmox_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
)

func TestUploadTemplateStreamsTheArchive(t *testing.T) {
	srv, c := setup(t)
	ctx := context.Background()
	body := bytes.Repeat([]byte("rootfs-bytes"), 100_000)
	sum := sha256.Sum256(body)
	volid, err := c.UploadTemplate(ctx, node, "local", "ghrm-abc.tar.zst", bytes.NewReader(body), int64(len(body)), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if volid != "local:vztmpl/ghrm-abc.tar.zst" {
		t.Fatalf("volid = %q", volid)
	}
	v, ok := srv.Volume(volid)
	if !ok || v.SHA256 != hex.EncodeToString(sum[:]) || v.Size != int64(len(body)) || v.Content != "vztmpl" {
		t.Fatalf("stored volume = %+v, %v", v, ok)
	}
	vols, err := c.StorageContent(ctx, node, "local", "vztmpl")
	if err != nil || len(vols) != 1 || vols[0].VolID != volid || vols[0].Size != int64(len(body)) {
		t.Fatalf("content = %+v, %v", vols, err)
	}
	if err := c.DeleteVolume(ctx, node, "local", volid); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Volume(volid); ok {
		t.Fatal("volume not deleted")
	}
}

func TestUploadTemplateChecksumMismatchFails(t *testing.T) {
	_, c := setup(t)
	_, err := c.UploadTemplate(context.Background(), node, "local", "x.tar.zst", strings.NewReader("abc"), 3, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want a checksum failure", err)
	}
}

func TestCreateConvertFirewallResize(t *testing.T) {
	srv, c := setup(t)
	ctx := context.Background()
	err := c.CreateLXC(ctx, node, proxmox.CreateLXCOptions{VMID: 951, OSTemplate: "local:vztmpl/ghrm-abc.tar.zst", Hostname: "ghrm-template",
		Pool: "ghrm", Storage: "local-lvm", RootFSGB: 16, Cores: 2, MemoryMB: 2048, Nameserver: "1.1.1.1", Bridge: "jobnet", Tags: []string{"ghrm-template", "ghrmtpl-abc"}})
	if err != nil {
		t.Fatal(err)
	}
	g, ok := srv.Guest(951)
	if !ok {
		t.Fatal("guest not created")
	}
	want := map[string]string{
		"ostemplate": "local:vztmpl/ghrm-abc.tar.zst", "unprivileged": "1", "features": "nesting=1", "ostype": "ubuntu",
		"nameserver": "1.1.1.1", "net0": "name=eth0,bridge=jobnet,ip=dhcp,firewall=1", "rootfs": "local-lvm:16", "pool": "ghrm",
		"cores": "2", "memory": "2048", "swap": "0", "hostname": "ghrm-template", "tags": "ghrm-template;ghrmtpl-abc",
	}
	for k, v := range want {
		if g.Config[k] != v {
			t.Errorf("config %s = %q, want %q", k, g.Config[k], v)
		}
	}
	if err := c.EnableFirewallGroup(ctx, node, 951, "gh-runner"); err != nil {
		t.Fatal(err)
	}
	if fw := srv.Firewall(951); !fw.Enabled || len(fw.Groups) != 1 || fw.Groups[0] != "gh-runner" {
		t.Fatalf("firewall = %+v", fw)
	}
	if err := c.ResizeLXCDisk(ctx, node, 951, "rootfs", 40); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.Guest(951); g.Config["rootfs_size"] != "40G" {
		t.Fatalf("resize = %+v", g.Config)
	}
	if err := c.ConvertToTemplate(ctx, node, 951); err != nil {
		t.Fatal(err)
	}
	if g, _ := srv.Guest(951); !g.Template {
		t.Fatal("not a template")
	}
}

func TestCreateLXCFailureIsATaskError(t *testing.T) {
	srv, c := setup(t)
	srv.FailCreate = true
	err := c.CreateLXC(context.Background(), node, proxmox.CreateLXCOptions{VMID: 952, OSTemplate: "local:vztmpl/x.tar.zst", Storage: "local-lvm", RootFSGB: 8, Cores: 1, MemoryMB: 512, Bridge: "jobnet"})
	var te *proxmox.TaskError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want a task error", err)
	}
}
