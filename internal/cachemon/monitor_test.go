package cachemon

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
