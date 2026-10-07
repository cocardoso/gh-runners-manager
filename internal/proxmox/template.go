package proxmox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Volume is one entry of a storage's content.
type Volume struct {
	VolID   string `json:"volid"`
	Size    int64  `json:"size"`
	Content string `json:"content"`
}

func storagePath(node, storage, suffix string) string {
	return fmt.Sprintf("/nodes/%s/storage/%s%s", url.PathEscape(node), url.PathEscape(storage), suffix)
}

// UploadTemplate streams an archive to storage as a container template ("vztmpl") and
// waits for Proxmox to verify its SHA-256 and store it. It returns the volume ID.
// The body is never buffered: the multipart envelope is written around the reader.
func (c *Client) UploadTemplate(ctx context.Context, node, storage, filename string, r io.Reader, size int64, sha256hex string) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	boundary := "ghrm" + hex.EncodeToString(b[:])
	var head strings.Builder
	for _, kv := range [][2]string{{"content", "vztmpl"}, {"checksum-algorithm", "sha256"}, {"checksum", sha256hex}} {
		fmt.Fprintf(&head, "--%s\r\nContent-Disposition: form-data; name=%q\r\n\r\n%s\r\n", boundary, kv[0], kv[1])
	}
	fmt.Fprintf(&head, "--%s\r\nContent-Disposition: form-data; name=\"filename\"; filename=%q\r\nContent-Type: application/octet-stream\r\n\r\n", boundary, filename)
	tail := "\r\n--" + boundary + "--\r\n"
	path := storagePath(node, storage, "/upload")
	body := io.MultiReader(strings.NewReader(head.String()), io.LimitReader(r, size), strings.NewReader(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, body)
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(head.Len()) + size + int64(len(tail))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	// Large uploads outlive the API client's timeout; the context bounds them instead.
	hc := *c.http
	hc.Timeout = 0
	var upid string
	if err := c.send(&hc, req, http.MethodPost, path, &upid); err != nil {
		return "", err
	}
	if err := c.WaitTask(ctx, node, upid); err != nil {
		return "", &TaskError{UPID: upid, Err: err}
	}
	return storage + ":vztmpl/" + filename, nil
}

// StorageContent lists a storage's volumes of one content type.
func (c *Client) StorageContent(ctx context.Context, node, storage, content string) ([]Volume, error) {
	var out []Volume
	err := c.do(ctx, http.MethodGet, storagePath(node, storage, "/content"), url.Values{"content": {content}}, &out)
	return out, err
}

// DeleteVolume deletes a volume (for example an uploaded template archive).
func (c *Client) DeleteVolume(ctx context.Context, node, storage, volid string) error {
	return c.maybeTask(ctx, node, http.MethodDelete, storagePath(node, storage, "/content/"+url.PathEscape(volid)), nil)
}

// maybeTask issues a request that answers either nothing or a task UPID, and waits for the task.
func (c *Client) maybeTask(ctx context.Context, node, method, path string, params url.Values) error {
	var out any
	if err := c.do(ctx, method, path, params, &out); err != nil {
		return err
	}
	if upid, ok := out.(string); ok && strings.HasPrefix(upid, "UPID:") {
		if err := c.WaitTask(ctx, node, upid); err != nil {
			return &TaskError{UPID: upid, Err: err}
		}
	}
	return nil
}

// CreateLXCOptions describes a container created from a template archive with the
// job settings of spec §8.3: unprivileged, nesting and keyctl, a firewalled NIC on the job network.
type CreateLXCOptions struct {
	VMID       int
	OSTemplate string // volid of the archive
	Hostname   string
	Pool       string
	Storage    string // root disk storage, e.g. local-lvm
	RootFSGB   int
	Cores      int
	MemoryMB   int
	Nameserver string
	Bridge     string // job VNet
	Tags       []string
}

// CreateLXC creates a container from an archive and waits for the task.
func (c *Client) CreateLXC(ctx context.Context, node string, o CreateLXCOptions) error {
	p := url.Values{
		"vmid":         {strconv.Itoa(o.VMID)},
		"ostemplate":   {o.OSTemplate},
		"rootfs":       {fmt.Sprintf("%s:%d", o.Storage, o.RootFSGB)},
		"cores":        {strconv.Itoa(o.Cores)},
		"memory":       {strconv.Itoa(o.MemoryMB)},
		"swap":         {"0"},
		"unprivileged": {"1"},
		"features":     {"nesting=1,keyctl=1"},
		"ostype":       {"ubuntu"},
		"net0":         {"name=eth0,bridge=" + o.Bridge + ",ip=dhcp,firewall=1"},
	}
	for k, v := range map[string]string{"hostname": o.Hostname, "pool": o.Pool, "nameserver": o.Nameserver, "tags": strings.Join(o.Tags, ";")} {
		if v != "" {
			p.Set(k, v)
		}
	}
	return c.postTask(ctx, node, http.MethodPost, fmt.Sprintf("/nodes/%s/lxc", url.PathEscape(node)), p)
}

// ConvertToTemplate turns a stopped container into a template.
func (c *Client) ConvertToTemplate(ctx context.Context, node string, vmid int) error {
	return c.maybeTask(ctx, node, http.MethodPost, lxcPath(node, vmid, "/template"), nil)
}

// EnableFirewallGroup enables the guest firewall and attaches a security group.
func (c *Client) EnableFirewallGroup(ctx context.Context, node string, vmid int, group string) error {
	if err := c.do(ctx, http.MethodPut, lxcPath(node, vmid, "/firewall/options"), url.Values{"enable": {"1"}}, nil); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, lxcPath(node, vmid, "/firewall/rules"), url.Values{"type": {"group"}, "action": {group}, "enable": {"1"}}, nil)
}

// ResizeLXCDisk grows a disk to sizeGB.
func (c *Client) ResizeLXCDisk(ctx context.Context, node string, vmid int, disk string, sizeGB int) error {
	return c.maybeTask(ctx, node, http.MethodPut, lxcPath(node, vmid, "/resize"), url.Values{"disk": {disk}, "size": {fmt.Sprintf("%dG", sizeGB)}})
}
