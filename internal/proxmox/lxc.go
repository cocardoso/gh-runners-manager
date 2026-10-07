package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// LXC is an entry of GET /nodes/{node}/lxc.
type LXC struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Tags     string `json:"tags"`
	Template int    `json:"template"`
}

// UnmarshalJSON accepts vmid and template as numbers or numeric strings;
// Proxmox VE versions differ in how they encode them for containers.
func (l *LXC) UnmarshalJSON(b []byte) error {
	var raw struct {
		VMID     json.RawMessage `json:"vmid"`
		Name     string          `json:"name"`
		Status   string          `json:"status"`
		Tags     string          `json:"tags"`
		Template json.RawMessage `json:"template"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	vmid, err := flexInt(raw.VMID)
	if err != nil {
		return fmt.Errorf("vmid: %w", err)
	}
	template, err := flexInt(raw.Template)
	if err != nil {
		return fmt.Errorf("template: %w", err)
	}
	*l = LXC{VMID: vmid, Name: raw.Name, Status: raw.Status, Tags: raw.Tags, Template: template}
	return nil
}

func flexInt(b json.RawMessage) (int, error) {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return 0, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %s", b)
	}
	return v, nil
}

// TagList splits the Proxmox tag string.
func (l LXC) TagList() []string {
	return strings.FieldsFunc(l.Tags, func(r rune) bool { return r == ';' || r == ',' || r == ' ' })
}

// HasTag reports whether the guest carries tag.
func (l LXC) HasTag(tag string) bool {
	for _, t := range l.TagList() {
		if t == tag {
			return true
		}
	}
	return false
}

// ListLXC lists the LXC guests on node.
func (c *Client) ListLXC(ctx context.Context, node string) ([]LXC, error) {
	var out []LXC
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/lxc", nil, &out)
	return out, err
}

// CloneOptions configures CloneLXC.
type CloneOptions struct {
	Hostname    string
	Description string
	Pool        string
	Full        bool // false = linked clone (requires a template)
}

// CloneLXC clones source into target and waits for the task.
func (c *Client) CloneLXC(ctx context.Context, node string, source, target int, opts CloneOptions) error {
	params := url.Values{"newid": {strconv.Itoa(target)}, "full": {"0"}}
	if opts.Full {
		params.Set("full", "1")
	}
	if opts.Hostname != "" {
		params.Set("hostname", opts.Hostname)
	}
	if opts.Description != "" {
		params.Set("description", opts.Description)
	}
	if opts.Pool != "" {
		params.Set("pool", opts.Pool)
	}
	return c.postTask(ctx, node, http.MethodPost, lxcPath(node, source, "/clone"), params)
}

// SetLXCConfig updates configuration options synchronously.
func (c *Client) SetLXCConfig(ctx context.Context, node string, vmid int, values url.Values) error {
	return c.do(ctx, http.MethodPut, lxcPath(node, vmid, "/config"), values, nil)
}

// LXCConfig returns the guest configuration.
func (c *Client) LXCConfig(ctx context.Context, node string, vmid int) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodGet, lxcPath(node, vmid, "/config"), nil, &out)
	return out, err
}

// StartLXC starts a guest and waits for the task.
func (c *Client) StartLXC(ctx context.Context, node string, vmid int) error {
	return c.postTask(ctx, node, http.MethodPost, lxcPath(node, vmid, "/status/start"), nil)
}

// StopLXC stops a guest immediately and waits for the task.
func (c *Client) StopLXC(ctx context.Context, node string, vmid int) error {
	return c.postTask(ctx, node, http.MethodPost, lxcPath(node, vmid, "/status/stop"), nil)
}

// DeleteLXC destroys a stopped guest, purging it from jobs and ACLs.
func (c *Client) DeleteLXC(ctx context.Context, node string, vmid int) error {
	params := url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}
	return c.postTask(ctx, node, http.MethodDelete, lxcPath(node, vmid, ""), params)
}

// DeleteLXCKeepACLs destroys a stopped guest without purge, so ACLs on its VMID stay
// (template VMIDs carry the token's permissions).
func (c *Client) DeleteLXCKeepACLs(ctx context.Context, node string, vmid int) error {
	params := url.Values{"destroy-unreferenced-disks": {"1"}}
	return c.postTask(ctx, node, http.MethodDelete, lxcPath(node, vmid, ""), params)
}

// LXCStatus is GET /nodes/{node}/lxc/{vmid}/status/current.
type LXCStatus struct {
	Status string `json:"status"`
	Mem    int64  `json:"mem"`
	MaxMem int64  `json:"maxmem"`
}

// LXCCurrentStatus returns the live status of a guest.
func (c *Client) LXCCurrentStatus(ctx context.Context, node string, vmid int) (LXCStatus, error) {
	var out LXCStatus
	err := c.do(ctx, http.MethodGet, lxcPath(node, vmid, "/status/current"), nil, &out)
	return out, err
}

// Interface is an entry of GET /nodes/{node}/lxc/{vmid}/interfaces.
type Interface struct {
	Name string `json:"name"`
	Inet string `json:"inet"`
}

// LXCInterfaces returns the network interfaces of a running guest.
func (c *Client) LXCInterfaces(ctx context.Context, node string, vmid int) ([]Interface, error) {
	var out []Interface
	err := c.do(ctx, http.MethodGet, lxcPath(node, vmid, "/interfaces"), nil, &out)
	return out, err
}
