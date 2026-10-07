package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// A Proxmox task warning is tied to the environment (or template) whose VMID it names.
func TestTaskWarningsPointAtTheirGuest(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "ghrm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_ = db.CreateEnvironment(ctx, store.Environment{ID: "old", ScaleSet: "lab", State: "destroyed", RuntimeRef: "900/old", MemoryMB: 512})
	_ = db.CreateEnvironment(ctx, store.Environment{ID: "new", ScaleSet: "lab", State: "destroying", RuntimeRef: "900/new", MemoryMB: 512})
	_ = db.CreateTemplate(ctx, store.Template{ID: "t1", State: store.TemplateReady, VMID: 952, SlimRelease: "1", RunnerVersion: "1", LayerVersion: "1"})

	refs, data := taskWarningRefs(ctx, db, proxmox.TaskWarnings{UPID: "UPID:pve:1:2:3:vzdestroy:900:ghrm@pve!ghrm:", Type: "vzdestroy", VMID: 900, Log: "WARN"})
	if refs.EnvironmentID != "new" || refs.ScaleSet != "lab" || data["vmid"] != 900 {
		t.Fatalf("refs %+v data %v; want the newest environment on VMID 900", refs, data)
	}
	_, data = taskWarningRefs(ctx, db, proxmox.TaskWarnings{Type: "vzdestroy", VMID: 952})
	if data["template_id"] != "t1" {
		t.Fatalf("data %v; want the template on VMID 952", data)
	}
}
