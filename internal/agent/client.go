package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
)

// MaxFrameText bounds one log line; longer lines are truncated with a marker.
const MaxFrameText = 64 << 10

// Options tune the client.
type Options struct {
	Interval   time.Duration // batch interval (default 250ms)
	MaxBatch   int           // frames per request (default 500)
	MaxQueue   int           // queued frames before the oldest logs are dropped (default 100000)
	MaxBytes   int           // encoded bytes per request (default 1 MiB, below the ingest's 4 MiB limit)
	MaxBackoff time.Duration // retry backoff cap (default 5s)
}

// Client queues frames and delivers them in order to the ingest.
type Client struct {
	base  string // ingest base URL
	url   string
	token string
	http  *http.Client
	opts  Options

	sendMu sync.Mutex // one request at a time: Run and Flush never interleave

	mu       sync.Mutex
	queue    []queued
	nextID   uint64
	inflight uint64 // highest frame id in the request being sent
	seq      map[string]int64
	seqBase  int64 // the client's start time in µs: a restarted agent continues above its predecessor
	dropped  int
	notify   chan struct{}
}

type queued struct {
	id    uint64
	frame ingest.Frame
}

// NewClient returns a client pinned to the bootstrap's certificate fingerprint.
func NewClient(b Bootstrap, opts Options) (*Client, error) {
	want, err := proxmox.ParseFingerprint(b.Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("agent: ingest fingerprint: %w", err)
	}
	if opts.Interval <= 0 {
		opts.Interval = 250 * time.Millisecond
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 500
	}
	if opts.MaxQueue <= 0 {
		opts.MaxQueue = 100000
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 5 * time.Second
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 1 << 20
	}
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // replaced by the pinned fingerprint check below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("agent: ingest sent no certificate")
			}
			got := sha256.Sum256(raw[0])
			if !bytes.Equal(got[:], want) {
				return fmt.Errorf("agent: ingest certificate fingerprint mismatch")
			}
			return nil
		},
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsCfg
	return &Client{
		base:    strings.TrimRight(b.URL, "/"),
		url:     strings.TrimRight(b.URL, "/") + ingest.FramesPath,
		token:   b.Token,
		http:    &http.Client{Transport: tr, Timeout: 30 * time.Second},
		opts:    opts,
		seq:     map[string]int64{},
		seqBase: time.Now().UnixMicro(),
		notify:  make(chan struct{}, 1),
	}, nil
}

func truncate(text string) string {
	if len(text) <= MaxFrameText {
		return text
	}
	return text[:MaxFrameText] + fmt.Sprintf(" …[truncated %d bytes]", len(text)-MaxFrameText)
}

// enqueue assigns the frame's sequence (per stream) and queue position under one lock,
// so sequence order always matches delivery order.
func (c *Client) enqueue(seqStream string, f ingest.Frame) {
	c.mu.Lock()
	if f.Time.IsZero() {
		f.Time = time.Now().UTC()
	}
	f.Text = truncate(f.Text)
	c.seq[seqStream]++
	f.Seq = c.seqBase + c.seq[seqStream]
	if len(c.queue) >= c.opts.MaxQueue {
		// Drop the oldest log or metric that is not part of the request in flight.
		for i, q := range c.queue {
			if q.id > c.inflight && (q.frame.Type == ingest.TypeLog || q.frame.Type == ingest.TypeMetric) {
				c.queue = append(c.queue[:i], c.queue[i+1:]...)
				c.dropped++
				break
			}
		}
	}
	c.nextID++
	c.queue = append(c.queue, queued{id: c.nextID, frame: f})
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

// Log queues a log line on stream ("agent", "runner" or "job").
func (c *Client) Log(stream, text string) {
	c.enqueue(stream, ingest.Frame{Type: ingest.TypeLog, Stream: stream, Text: text})
}

// Event queues a lifecycle event. Events share the "agent" stream's sequence.
func (c *Client) Event(name string, data map[string]any) {
	c.enqueue("agent", ingest.Frame{Type: ingest.TypeEvent, Name: name, Data: data})
}

// Metric queues a resource sample.
func (c *Client) Metric(cpuUsec, memBytes int64) {
	c.enqueue("metrics", ingest.Frame{Type: ingest.TypeMetric, CPUUsec: cpuUsec, MemBytes: memBytes})
}

// Dropped returns how many frames were dropped because the queue was full.
func (c *Client) Dropped() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}

func (c *Client) pending() []ingest.Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ingest.Frame, len(c.queue))
	for i, q := range c.queue {
		out[i] = q.frame
	}
	return out
}

// remove deletes the given frame ids from the queue.
func (c *Client) remove(ids map[uint64]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.queue[:0]
	for _, q := range c.queue {
		if !ids[q.id] {
			kept = append(kept, q)
		}
	}
	c.queue = kept
	c.inflight = 0
}

// sendOnce delivers a batch from the head of the queue, bounded by count and bytes.
// A 400 drops the batch (it can never succeed); a 413 splits it.
func (c *Client) sendOnce(ctx context.Context) (int, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	limit := c.opts.MaxBatch
	for {
		c.mu.Lock()
		var body bytes.Buffer
		ids := map[uint64]bool{}
		for _, q := range c.queue {
			if len(ids) >= limit {
				break
			}
			line, err := json.Marshal(q.frame)
			if err != nil {
				continue
			}
			if len(ids) > 0 && body.Len()+len(line)+1 > c.opts.MaxBytes {
				break
			}
			body.Write(line)
			body.WriteByte('\n')
			ids[q.id] = true
			c.inflight = q.id
		}
		c.mu.Unlock()
		if len(ids) == 0 {
			return 0, nil
		}
		status, err := c.post(ctx, &body)
		switch {
		case err != nil:
			c.mu.Lock()
			c.inflight = 0
			c.mu.Unlock()
			return 0, err
		case status == http.StatusOK:
			c.remove(ids)
			c.reportDropped()
			return len(ids), nil
		case status == http.StatusRequestEntityTooLarge && len(ids) > 1:
			limit = len(ids) / 2
			continue
		case status == http.StatusRequestEntityTooLarge || status == http.StatusBadRequest:
			c.remove(ids)
			c.Event(ingest.EventFramesRejected, map[string]any{"count": len(ids), "status": status})
			return len(ids), nil
		default:
			c.mu.Lock()
			c.inflight = 0
			c.mu.Unlock()
			return 0, fmt.Errorf("agent: ingest answered %d", status)
		}
	}
}

func (c *Client) post(ctx context.Context, body *bytes.Buffer) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (c *Client) reportDropped() {
	c.mu.Lock()
	dropped := c.dropped
	c.dropped = 0
	c.mu.Unlock()
	if dropped > 0 {
		c.Event(ingest.EventFramesDropped, map[string]any{"count": dropped})
	}
}

// Run delivers queued frames until ctx ends.
func (c *Client) Run(ctx context.Context) {
	backoff := c.opts.Interval
	t := time.NewTicker(c.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.notify:
			time.Sleep(c.opts.Interval / 5) // let a burst accumulate into one batch
		}
		for {
			n, err := c.sendOnce(ctx)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff = min(backoff*2, c.opts.MaxBackoff)
				continue
			}
			backoff = c.opts.Interval
			if n == 0 {
				break
			}
		}
	}
}

// Flush delivers everything queued, retrying until ctx ends.
func (c *Client) Flush(ctx context.Context) error {
	backoff := c.opts.Interval
	for {
		if len(c.pending()) == 0 {
			return nil
		}
		if _, err := c.sendOnce(ctx); err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("agent: flush: %w (last error: %v)", ctx.Err(), err)
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, c.opts.MaxBackoff)
		}
	}
}

// request sends an authenticated request to an ingest endpoint. Large transfers are bounded
// by ctx, not by the client timeout.
func (c *Client) request(ctx context.Context, method, path string, body io.Reader, size int64, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if size >= 0 && body != nil {
		req.ContentLength = size
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	hc := *c.http
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("agent: %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// GetJSON decodes an ingest endpoint's JSON answer.
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	resp, err := c.request(ctx, http.MethodGet, path, nil, -1, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// Download copies an ingest endpoint's body to w.
func (c *Client) Download(ctx context.Context, path string, w io.Writer) error {
	resp, err := c.request(ctx, http.MethodGet, path, nil, -1, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

// Send uploads a body to an ingest endpoint.
func (c *Client) Send(ctx context.Context, method, path string, body io.Reader, size int64, hdr map[string]string) error {
	resp, err := c.request(ctx, method, path, body, size, hdr)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}
