// Package proxmox is a small client for the parts of the Proxmox VE API that ghrm uses.
// It authenticates with API tokens only.
package proxmox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
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
	InsecureSkipVerify bool         // accept any certificate (opt-in; prefer TLSFingerprint)
	TLSFingerprint     string       // SHA-256 of the server certificate, hex with optional colons
	HTTPClient         *http.Client // optional; when set, InsecureSkipVerify is ignored
}

// Client talks to one Proxmox VE API endpoint.
type Client struct {
	base string
	auth string
	http *http.Client

	// PollInterval is the delay between task status polls.
	PollInterval time.Duration
	// OnTaskWarnings, when set, receives every task that succeeded with warnings.
	OnTaskWarnings func(TaskWarnings)
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

// Is lets errors.Is(err, ErrNotFound) match missing guests: a 404, or Proxmox's
// "Configuration file '...' does not exist". Other "does not exist" reasons (a
// storage, a bridge) are configuration errors, not a missing guest.
func (e *APIError) Is(target error) bool {
	if target != ErrNotFound {
		return false
	}
	msg := e.Status + " " + e.Body
	return e.StatusCode == http.StatusNotFound ||
		(strings.Contains(msg, "Configuration file") && strings.Contains(msg, "does not exist"))
}

// ParseFingerprint decodes a SHA-256 certificate fingerprint written as hex,
// with or without colons (as Proxmox shows it).
func ParseFingerprint(s string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
	if err != nil || len(raw) != sha256.Size {
		return nil, fmt.Errorf("proxmox: invalid SHA-256 fingerprint %q", s)
	}
	return raw, nil
}

// New returns a Client for cfg.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("proxmox: invalid URL %q", cfg.URL)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		tlsCfg := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in for self-signed homelab certificates
		if cfg.TLSFingerprint != "" {
			want, err := ParseFingerprint(cfg.TLSFingerprint)
			if err != nil {
				return nil, err
			}
			// The chain is not verified; the leaf certificate must match the pinned fingerprint instead.
			tlsCfg.InsecureSkipVerify = true //nolint:gosec // replaced by VerifyPeerCertificate below
			tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return errors.New("proxmox: server sent no certificate")
				}
				got := sha256.Sum256(rawCerts[0])
				if !bytes.Equal(got[:], want) {
					return fmt.Errorf("proxmox: certificate fingerprint mismatch: got %X", got)
				}
				return nil
			}
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = tlsCfg
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
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return c.send(c.http, req, method, path, out)
}

// send authenticates req, sends it and decodes the "data" envelope into out.
func (c *Client) send(hc *http.Client, req *http.Request, method, path string, out any) error {
	req.Header.Set("Authorization", c.auth)
	resp, err := hc.Do(req)
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
