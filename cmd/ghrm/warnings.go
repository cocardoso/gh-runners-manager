package main

import (
	"context"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// taskWarningRefs ties a Proxmox task warning to the environment or template version
// on its VMID, so the event shows up where the operator looks.
func taskWarningRefs(ctx context.Context, db *store.Store, w proxmox.TaskWarnings) (events.Refs, map[string]any) {
	data := map[string]any{"upid": w.UPID, "type": w.Type, "vmid": w.VMID, "log": w.Log}
	var refs events.Refs
	if w.VMID == 0 {
		return refs, data
	}
	if e, err := db.FindEnvironmentByVMID(ctx, w.VMID); err == nil {
		refs = events.Refs{ScaleSet: e.ScaleSet, EnvironmentID: e.ID, JobID: e.JobID}
	}
	if list, err := db.ListTemplates(ctx); err == nil {
		for _, t := range list {
			if t.VMID == w.VMID {
				data["template_id"] = t.ID
				break
			}
		}
	}
	return refs, data
}
