// Package proxmox is a small client for the parts of the Proxmox VE API that ghrm uses.
// It authenticates with API tokens only.
package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound matches API errors for guests or objects that do not exist.
var ErrNotFound = errors.New("proxmox: not found")

// Config configures a Client.
type Config struct {
	URL                string // e.g. https://pve.example.test:8006
	TokenID            string // user@realm!tokenname
	TokenSecret        string
	InsecureSkipVerify bool         // accept self-signed certificates (opt-in)
	HTTPClient         *http.Client // optional; when set, InsecureSkipVerify is ignored
}

// Client talks to one Proxmox VE API endpoint.
type Client struct {
	base string
	auth string
	http *http.Client

	// PollInterval is the delay between task status polls.
	PollInterval time.Duration
}

// APIError is a non-2xx API response.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Status     string // full status line; Proxmox puts the reason here
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("proxmox %s %s: %s %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

// Is lets errors.Is(err, ErrNotFound) match missing objects.
func (e *APIError) Is(target error) bool {
	return target == ErrNotFound &&
		(e.StatusCode == http.StatusNotFound || strings.Contains(e.Status+" "+e.Body, "does not exist"))
}

// New returns a Client for cfg.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("proxmox: invalid URL %q", cfg.URL)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in for self-signed homelab certificates
		hc = &http.Client{Transport: tr, Timeout: 60 * time.Second}
	}
	return &Client{
		base:         strings.TrimRight(u.String(), "/") + "/api2/json",
		auth:         "PVEAPIToken=" + cfg.TokenID + "=" + cfg.TokenSecret,
		http:         hc,
		PollInterval: 500 * time.Millisecond,
	}, nil
}

// do performs a request. params go in the query string for GET/DELETE and in a
// form body otherwise. When out is non-nil, the response's "data" field is decoded into it.
func (c *Client) do(ctx context.Context, method, path string, params url.Values, out any) error {
	target := c.base + path
	var body io.Reader
	if len(params) > 0 {
		if method == http.MethodGet || method == http.MethodDelete {
			target += "?" + params.Encode()
		} else {
			body = strings.NewReader(params.Encode())
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("proxmox %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("proxmox %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Status: resp.Status, Body: string(raw)}
	}
	if out == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("proxmox %s %s: decode: %w", method, path, err)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("proxmox %s %s: decode data: %w", method, path, err)
	}
	return nil
}

func lxcPath(node string, vmid int, suffix string) string {
	return fmt.Sprintf("/nodes/%s/lxc/%d%s", url.PathEscape(node), vmid, suffix)
}
