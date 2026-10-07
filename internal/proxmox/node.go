package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// NodeMemory is the memory section of GET /nodes/{node}/status (bytes).
type NodeMemory struct {
	Total     int64 `json:"total"`
	Free      int64 `json:"free"`
	Available int64 `json:"available"`
}

// NodeStatus is GET /nodes/{node}/status.
type NodeStatus struct {
	Memory NodeMemory `json:"memory"`
}

// NodeStatus returns node resource usage.
func (c *Client) NodeStatus(ctx context.Context, node string) (NodeStatus, error) {
	var out NodeStatus
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/status", nil, &out)
	return out, err
}

// ThinPool is an entry of GET /nodes/{node}/disks/lvmthin (bytes).
type ThinPool struct {
	LV           string `json:"lv"`
	Size         int64  `json:"lv_size"`
	Used         int64  `json:"used"`
	MetadataSize int64  `json:"metadata_size"`
	MetadataUsed int64  `json:"metadata_used"`
}

// ThinPools lists LVM thin pools on node.
func (c *Client) ThinPools(ctx context.Context, node string) ([]ThinPool, error) {
	var out []ThinPool
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/disks/lvmthin", nil, &out)
	return out, err
}

// StorageStatus is GET /nodes/{node}/storage/{storage}/status (bytes).
type StorageStatus struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

// StorageStatus returns the usage of a storage. It needs only Datastore.Audit on the storage.
func (c *Client) StorageStatus(ctx context.Context, node, storage string) (StorageStatus, error) {
	var out StorageStatus
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/storage/"+url.PathEscape(storage)+"/status", nil, &out)
	return out, err
}

// VMIDAvailable reports whether no guest in the cluster uses vmid. It sees guests
// the token has no permission on, unlike listing endpoints.
func (c *Client) VMIDAvailable(ctx context.Context, vmid int) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/cluster/nextid", url.Values{"vmid": {strconv.Itoa(vmid)}}, nil)
	var apiErr *APIError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && strings.Contains(apiErr.Status+apiErr.Body, "already exists"):
		return false, nil
	default:
		return false, err
	}
}
