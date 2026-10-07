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

// Options tune the client.
type Options struct {
	Interval   time.Duration // batch interval (default 250ms)
	MaxBatch   int           // frames per request (default 500)
	MaxQueue   int           // queued frames before the oldest logs are dropped (default 100000)
	MaxBackoff time.Duration // retry backoff cap (default 5s)
}

// Client queues frames and delivers them in order to the ingest.
type Client struct {
	url   string
	token string
	http  *http.Client
	opts  Options

	mu      sync.Mutex
	queue   []ingest.Frame
	seq     map[string]int64
	dropped int
	notify  chan struct{}
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
		url:    strings.TrimRight(b.URL, "/") + ingest.FramesPath,
		token:  b.Token,
		http:   &http.Client{Transport: tr, Timeout: 30 * time.Second},
		opts:   opts,
		seq:    map[string]int64{},
		notify: make(chan struct{}, 1),
	}, nil
}

func (c *Client) enqueue(f ingest.Frame) {
	c.mu.Lock()
	if f.Time.IsZero() {
		f.Time = time.Now().UTC()
	}
	if len(c.queue) >= c.opts.MaxQueue {
		for i, q := range c.queue {
			if q.Type == ingest.TypeLog || q.Type == ingest.TypeMetric {
				c.queue = append(c.queue[:i], c.queue[i+1:]...)
				c.dropped++
				break
			}
		}
	}
	c.queue = append(c.queue, f)
	c.mu.Unlock()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

func (c *Client) next(stream string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq[stream]++
	return c.seq[stream]
}

// Log queues a log line on stream ("agent", "runner" or "job").
func (c *Client) Log(stream, text string) {
	c.enqueue(ingest.Frame{Type: ingest.TypeLog, Stream: stream, Seq: c.next(stream), Text: text})
}

// Event queues a lifecycle event. Events share the "agent" stream's sequence.
func (c *Client) Event(name string, data map[string]any) {
	c.enqueue(ingest.Frame{Type: ingest.TypeEvent, Name: name, Seq: c.next("agent"), Data: data})
}

// Metric queues a resource sample.
func (c *Client) Metric(cpuUsec, memBytes int64) {
	c.enqueue(ingest.Frame{Type: ingest.TypeMetric, Seq: c.next("metrics"), CPUUsec: cpuUsec, MemBytes: memBytes})
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
	return append([]ingest.Frame(nil), c.queue...)
}

// sendOnce delivers up to MaxBatch frames from the head of the queue.
func (c *Client) sendOnce(ctx context.Context) (int, error) {
	c.mu.Lock()
	n := min(len(c.queue), c.opts.MaxBatch)
	batch := append([]ingest.Frame(nil), c.queue[:n]...)
	c.mu.Unlock()
	if n == 0 {
		return 0, nil
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for _, f := range batch {
		if err := enc.Encode(f); err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, &body)
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
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("agent: ingest answered %s", resp.Status)
	}
	c.mu.Lock()
	// Frames dropped meanwhile were older than the batch only if they were in it;
	// drop the delivered prefix by identity of position.
	c.queue = c.queue[min(n, len(c.queue)):]
	dropped := c.dropped
	c.dropped = 0
	c.mu.Unlock()
	if dropped > 0 {
		c.Event(ingest.EventFramesDropped, map[string]any{"count": dropped})
	}
	return n, nil
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
			if n < c.opts.MaxBatch {
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
