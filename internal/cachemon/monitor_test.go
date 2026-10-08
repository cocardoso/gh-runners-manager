package cachemon

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

func serve(t *testing.T, body string) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	return srv, p
}

type harness struct {
	m   *Monitor
	db  *store.Store
	reg *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fixture, err := os.ReadFile("testdata/registry-proxy-metrics.txt")
	if err != nil {
		t.Fatal(err)
	}
	reg, regPort := serve(t, string(fixture))
	_, expPort := serve(t, "# TYPE ghrm_cache_disk_used_bytes gauge\nghrm_cache_disk_used_bytes 5e+09\nghrm_cache_disk_budget_bytes 1.073741824e+11\n")
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := config.Cache{Address: "127.0.0.1", Ports: map[string]int{}, MetricsPorts: map[string]int{}, ExporterPort: expPort}
	for i, o := range config.CacheOrigins {
		c.Ports[o] = 5000 + i
		c.MetricsPorts[o] = regPort // every origin answers with the captured metrics
	}
	return &harness{m: &Monitor{Cache: c, Recorder: events.NewRecorder(db, events.NewBus(), nil)}, db: db, reg: reg}
}

func TestMonitorReadsOriginsAndDisk(t *testing.T) {
	h := newHarness(t)
	h.m.Poll(context.Background())
	s := h.m.Status()
	if !s.Enabled || !s.Up || len(s.Origins) != 4 || s.DiskUsed != 5e9 || s.DiskBudget != 100<<30 || s.CheckedAt.IsZero() {
		t.Fatalf("status = %+v", s)
	}
	o := s.Origins[0]
	// From the real registry's metrics: one blob served from the cache, one fetched from the origin.
	if o.Origin != "docker.io" || !o.Up || o.BlobHits != 1 || o.BlobMisses != 1 || o.ServedBytes != 4452326 || o.PulledBytes != 2226163 {
		t.Fatalf("docker.io = %+v", o)
	}
}

func TestMonitorRecordsDownAndUp(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.m.Poll(ctx)
	h.reg.Close()
	h.m.Poll(ctx)
	h.m.Poll(ctx) // down twice in a row
	if s := h.m.Status(); s.Up || s.Origins[1].Up || s.Origins[1].Error == "" {
		t.Fatalf("status after the cache went away = %+v", s)
	}
	h.m.Poll(ctx) // still down: no second event
	evs, _ := h.db.ListEvents(ctx, store.EventFilter{})
	downs := 0
	for _, e := range evs {
		if e.Kind == "cache.down" {
			downs++
		}
	}
	if downs != 1 {
		t.Fatalf("cache.down events = %d, want 1", downs)
	}
}

func TestMonitorKeepsTheLastCountersWhileDown(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)
	h.m.Now = func() time.Time { return at }
	h.m.Poll(ctx)
	h.reg.Close()
	at = at.Add(30 * time.Second)
	wentDown := at
	h.m.Poll(ctx)
	at = at.Add(30 * time.Second)
	h.m.Poll(ctx)
	at = at.Add(30 * time.Second)
	h.m.Poll(ctx)
	s := h.m.Status()
	// Counters going to zero would read as a counter reset in Prometheus.
	if o := s.Origins[0]; o.Up || o.BlobHits != 1 || o.BlobMisses != 1 || o.ServedBytes != 4452326 {
		t.Fatalf("docker.io while down = %+v; want the last counters kept", o)
	}
	if !s.DownSince.Equal(wentDown) || !s.CheckedAt.Equal(at) {
		t.Fatalf("down since %v (want %v), checked at %v", s.DownSince, wentDown, s.CheckedAt)
	}
}

func TestOneFailedCheckIsNotAnOutage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	fail := true
	reg, port := serve(t, "")
	reg.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		b, _ := os.ReadFile("testdata/registry-proxy-metrics.txt")
		_, _ = w.Write(b)
	})
	h.m.Cache.MetricsPorts["ghcr.io"] = port
	h.m.Poll(ctx)
	if s := h.m.Status(); !s.Up || s.Origins[1].Up || s.Origins[1].Error == "" {
		t.Fatalf("after one failed check = %+v; the origin shows the error, the cache is not down yet", s)
	}
	fail = false
	h.m.Poll(ctx)
	evs, _ := h.db.ListEvents(ctx, store.EventFilter{})
	for _, e := range evs {
		if strings.HasPrefix(e.Kind, "cache.") {
			t.Fatalf("event %s for a single failed check", e.Kind)
		}
	}
}

func TestOriginsArePolledTogether(t *testing.T) {
	h := newHarness(t)
	slow, port := serve(t, "")
	slow.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		b, _ := os.ReadFile("testdata/registry-proxy-metrics.txt")
		_, _ = w.Write(b)
	})
	for _, o := range config.CacheOrigins {
		h.m.Cache.MetricsPorts[o] = port
	}
	start := time.Now()
	h.m.Poll(context.Background())
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Fatalf("poll took %v; four slow origins must be checked in parallel", d)
	}
	if !h.m.Status().Up {
		t.Fatal("slow origins still answer")
	}
}

func TestDisabledCacheIsNotPolled(t *testing.T) {
	m := &Monitor{}
	m.Poll(context.Background())
	if s := m.Status(); s.Enabled || s.Up || len(s.Origins) != 0 {
		t.Fatalf("status = %+v", s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	m.Run(ctx) // returns at once without a cache
}
