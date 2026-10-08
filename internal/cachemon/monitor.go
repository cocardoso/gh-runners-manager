// Package cachemon watches the registry cache on the job network (M6): whether each
// origin's proxy answers, how many blobs it served from the cache, and the cache's disk
// usage. It reads the proxies' Prometheus metrics and the cache's disk exporter.
package cachemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/events"
)

// Interval is how often the cache is checked.
const Interval = 30 * time.Second

// OriginStatus is one origin's proxy.
type OriginStatus struct {
	Origin      string  `json:"origin"`
	Up          bool    `json:"up"`
	Error       string  `json:"error,omitempty"`
	BlobHits    float64 `json:"blob_hits"`    // blobs served from the cache
	BlobMisses  float64 `json:"blob_misses"`  // blobs fetched from the origin
	ServedBytes float64 `json:"served_bytes"` // bytes sent to jobs
	PulledBytes float64 `json:"pulled_bytes"` // bytes fetched from the origin
}

// Status is the cache as last seen.
type Status struct {
	Enabled    bool           `json:"enabled"`
	Address    string         `json:"address,omitempty"`
	Up         bool           `json:"up"`
	Origins    []OriginStatus `json:"origins"`
	DiskUsed   int64          `json:"disk_used_bytes"`
	DiskBudget int64          `json:"disk_budget_bytes"`
	CheckedAt  time.Time      `json:"checked_at"`
	DownSince  time.Time      `json:"down_since,omitzero"` // first check that found it down
}

// Monitor polls the cache.
type Monitor struct {
	Cache    config.Cache
	HTTP     *http.Client
	Recorder *events.Recorder
	Now      func() time.Time

	mu     sync.Mutex
	status Status
	seen   bool
}

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Monitor) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// Status returns the last check.
func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.Enabled, s.Address = m.Cache.Enabled(), m.Cache.Address
	s.Origins = append([]OriginStatus{}, s.Origins...)
	return s
}

// Run checks the cache every Interval until ctx ends; without a cache it returns.
func (m *Monitor) Run(ctx context.Context) {
	if !m.Cache.Enabled() {
		return
	}
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		m.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// metrics fetches a Prometheus text page as name{labels} -> value.
func (m *Monitor) metrics(ctx context.Context, port int) (map[string]float64, error) {
	url := "http://" + net.JoinHostPort(m.Cache.Address, strconv.Itoa(port)) + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	out := map[string]float64{}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		if i < 0 {
			continue
		}
		if v, err := strconv.ParseFloat(line[i+1:], 64); err == nil {
			out[line[:i]] = v
		}
	}
	return out, sc.Err()
}

// Poll checks every origin and the disk exporter once.
func (m *Monitor) Poll(ctx context.Context) {
	if !m.Cache.Enabled() {
		return
	}
	m.mu.Lock()
	prev := m.status
	m.mu.Unlock()
	s := Status{Up: true, CheckedAt: m.now(), DiskUsed: prev.DiskUsed, DiskBudget: prev.DiskBudget}
	for i, origin := range config.CacheOrigins {
		o := OriginStatus{Origin: origin}
		got, err := m.metrics(ctx, m.Cache.MetricsPorts[origin])
		if err != nil {
			// The last counters stay: dropping to zero would read as a counter reset.
			if i < len(prev.Origins) {
				o = prev.Origins[i]
			}
			o.Up, o.Error = false, err.Error()
			s.Up = false
		} else {
			o.Up = true
			o.BlobHits = got[`registry_proxy_hits_total{type="blob"}`]
			o.BlobMisses = got[`registry_proxy_misses_total{type="blob"}`]
			o.ServedBytes = got[`registry_proxy_pushed_bytes_total{type="blob"}`]
			o.PulledBytes = got[`registry_proxy_pulled_bytes_total{type="blob"}`]
		}
		s.Origins = append(s.Origins, o)
	}
	if disk, err := m.metrics(ctx, m.Cache.ExporterPort); err == nil {
		s.DiskUsed, s.DiskBudget = int64(disk["ghrm_cache_disk_used_bytes"]), int64(disk["ghrm_cache_disk_budget_bytes"])
	}

	m.mu.Lock()
	wasUp, seen := m.status.Up, m.seen
	if !s.Up {
		s.DownSince = s.CheckedAt
		if seen && !wasUp {
			s.DownSince = m.status.DownSince
		}
	}
	m.status, m.seen = s, true
	m.mu.Unlock()
	if m.Recorder == nil || (seen && wasUp == s.Up) {
		return
	}
	switch {
	case !s.Up:
		var down []string
		for _, o := range s.Origins {
			if !o.Up {
				down = append(down, o.Origin)
			}
		}
		_, _ = m.Recorder.Warn(ctx, "cache.down", "the registry cache does not answer for "+strings.Join(down, ", ")+"; jobs pull from the origins",
			events.Refs{}, map[string]any{"address": m.Cache.Address})
	case seen:
		_, _ = m.Recorder.Info(ctx, "cache.up", "the registry cache answers again", events.Refs{}, map[string]any{"address": m.Cache.Address})
	}
}
