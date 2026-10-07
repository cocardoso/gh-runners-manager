// Package metrics exposes the control plane's Prometheus metrics (spec §12.3). Fleet
// gauges and totals are read from the database at scrape time, so they survive restarts;
// stage durations are observed as environments change state.
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// ScaleSets reports the live state of the scale sets (the controller).
type ScaleSets interface {
	ScaleSets(ctx context.Context) []controller.ScaleSetStatus
}

// Metrics owns a registry with the ghrm collectors.
type Metrics struct {
	reg    *prometheus.Registry
	stages *prometheus.HistogramVec
}

var (
	envDesc      = prometheus.NewDesc("ghrm_environments", "Environments that are not destroyed, by state.", []string{"scale_set", "state"}, nil)
	failDesc     = prometheus.NewDesc("ghrm_environment_failures_total", "Environments that failed, by stage.", []string{"stage"}, nil)
	jobsDesc     = prometheus.NewDesc("ghrm_jobs_total", "Completed jobs, by result.", []string{"scale_set", "result"}, nil)
	desiredDesc  = prometheus.NewDesc("ghrm_scale_set_desired", "Jobs GitHub assigned to the scale set (queue depth).", []string{"scale_set"}, nil)
	listenDesc   = prometheus.NewDesc("ghrm_scale_set_listening", "1 while the scale set's listener is connected.", []string{"scale_set"}, nil)
	buildsDesc   = prometheus.NewDesc("ghrm_template_builds_total", "Template versions by outcome (failed, ready, active, retired, building...).", []string{"result"}, nil)
	scrapeErrors = prometheus.NewDesc("ghrm_metrics_scrape_errors", "Database reads that failed during this scrape.", nil, nil)
)

type collector struct {
	db   *store.Store
	sets ScaleSets
}

func (c collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{envDesc, failDesc, jobsDesc, desiredDesc, listenDesc, buildsDesc, scrapeErrors} {
		ch <- d
	}
}

func (c collector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errs := 0
	emit := func(d *prometheus.Desc, t prometheus.ValueType, rows []store.Count, err error, labels func(store.Count) []string) {
		if err != nil {
			errs++
			return
		}
		for _, r := range rows {
			ch <- prometheus.MustNewConstMetric(d, t, float64(r.N), labels(r)...)
		}
	}
	rows, err := c.db.CountEnvironmentsByState(ctx)
	emit(envDesc, prometheus.GaugeValue, rows, err, func(r store.Count) []string { return []string{r.ScaleSet, r.Key} })
	rows, err = c.db.CountFailuresByStage(ctx)
	emit(failDesc, prometheus.CounterValue, rows, err, func(r store.Count) []string { return []string{r.Key} })
	rows, err = c.db.CountCompletedJobs(ctx)
	emit(jobsDesc, prometheus.CounterValue, rows, err, func(r store.Count) []string { return []string{r.ScaleSet, r.Key} })
	rows, err = c.db.CountTemplatesByState(ctx)
	emit(buildsDesc, prometheus.CounterValue, rows, err, func(r store.Count) []string { return []string{r.Key} })
	for _, s := range c.sets.ScaleSets(ctx) {
		ch <- prometheus.MustNewConstMetric(desiredDesc, prometheus.GaugeValue, float64(s.Desired), s.Name)
		listening := 0.0
		if s.Listening {
			listening = 1
		}
		ch <- prometheus.MustNewConstMetric(listenDesc, prometheus.GaugeValue, listening, s.Name)
	}
	ch <- prometheus.MustNewConstMetric(scrapeErrors, prometheus.GaugeValue, float64(errs))
}

// New returns the metrics of a control plane.
func New(db *store.Store, sets ScaleSets, version string) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{reg: reg, stages: prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ghrm_stage_duration_seconds",
		Help:    "How long environments stay in each state.",
		Buckets: []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 1800, 3600, 7200},
	}, []string{"stage"})}
	info := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ghrm_build_info", Help: "The running ghrm version."}, []string{"version"})
	info.WithLabelValues(version).Set(1)
	reg.MustRegister(collector{db: db, sets: sets}, m.stages, info,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// ObserveStage implements controller.StageObserver.
func (m *Metrics) ObserveStage(stage string, d time.Duration) {
	m.stages.WithLabelValues(stage).Observe(d.Seconds())
}

// Handler serves the metrics in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}
