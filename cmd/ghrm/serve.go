package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/api"
	"github.com/cocardoso/gh-runners-manager/internal/backup"
	"github.com/cocardoso/gh-runners-manager/internal/cachemon"
	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/controller"
	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/github"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/metrics"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/retention"
	"github.com/cocardoso/gh-runners-manager/internal/runtime/proxmoxlxc"
	"github.com/cocardoso/gh-runners-manager/internal/secrets"
	"github.com/cocardoso/gh-runners-manager/internal/settings"
	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/internal/template"
	"github.com/cocardoso/gh-runners-manager/internal/version"
)

var (
	_ controller.GitHub     = (*github.Client)(nil)
	_ ingest.TokenResolver  = (*controller.Controller)(nil)
	_ ingest.EventSink      = (*controller.Controller)(nil)
	_ api.Controller        = (*controller.Controller)(nil)
	_ ingest.BuildService   = (*template.Service)(nil)
	_ template.Environments = (*controller.Controller)(nil)

	_ controller.FirewallGatedSource = (*template.Service)(nil)
)

// runtimeTemplates maps the templates configuration to the proxmox-lxc runtime.
func runtimeTemplates(cfg *config.Config) proxmoxlxc.TemplateConfig {
	t := cfg.Templates
	return proxmoxlxc.TemplateConfig{VMIDStart: t.VMIDRange.Start, VMIDEnd: t.VMIDRange.End, Storage: t.Storage, RootFSGB: t.RootFSGB,
		Nameserver: t.Nameserver, Bridge: t.Bridge, FirewallGroup: t.FirewallGroup}
}

// serve runs the control plane until ctx ends.
func serve(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "/etc/ghrm/ghrm.yaml", "path to the configuration file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err == nil {
		err = cfg.ValidateServe()
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	if err := runServe(ctx, cfg, logger); err != nil {
		logger.Error("serve failed", "error", err)
		return 1
	}
	return 0
}

func runServe(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "ghrm.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	vault, err := secrets.OpenVault(ctx, db, cfg.SecretKeyFile)
	if err != nil {
		return err
	}
	if err := cfg.ResolveVaultSecrets(ctx, vault.Get); err != nil {
		return err
	}
	if err := cfg.ValidateSecrets(); err != nil {
		return err
	}

	advertise, _ := url.Parse(cfg.Ingest.AdvertiseURL)
	cert, fingerprint, err := ingest.LoadOrCreateCert(filepath.Join(cfg.DataDir, "tls"), []string{advertise.Hostname()})
	if err != nil {
		return err
	}

	p := cfg.Proxmox
	pc, err := proxmox.New(proxmox.Config{URL: p.URL, TokenID: p.TokenID, TokenSecret: p.TokenSecret,
		InsecureSkipVerify: p.InsecureSkipVerify, TLSFingerprint: p.TLSFingerprint})
	if err != nil {
		return err
	}
	rt := proxmoxlxc.New(pc, proxmoxlxc.Config{Node: p.Node, TemplateVMID: p.TemplateVMID, Pool: p.Pool,
		VMIDStart: p.VMIDRange.Start, VMIDEnd: p.VMIDRange.End, ThinPool: p.ThinPool, Storage: p.Storage,
		FirewallSettle: p.FirewallSettle.Std(), Templates: runtimeTemplates(cfg)})

	bus := events.NewBus()
	rec := events.NewRecorder(db, bus, nil)
	// A task that succeeds with warnings (e.g. a destroy whose disk was still in use)
	// can leave residue on the host that later breaks new guests; surface it.
	pc.OnTaskWarnings = func(w proxmox.TaskWarnings) {
		logger.Warn("proxmox task finished with warnings", "upid", w.UPID, "type", w.Type, "vmid", w.VMID, "log", w.Log)
		wctx := context.WithoutCancel(ctx)
		refs, data := taskWarningRefs(wctx, db, w)
		_, _ = rec.Warn(wctx, "proxmox.task_warnings",
			fmt.Sprintf("Proxmox %s of %d finished with warnings; check the host for leftovers", w.Type, w.VMID), refs, data)
	}
	signIn, err := newAuth(ctx, db, cfg.DataDir, logger)
	if err != nil {
		return err
	}
	logStore := logs.New(filepath.Join(cfg.DataDir, "logs"), db)
	reg, err := settings.New(ctx, cfg, db, vault)
	if err != nil {
		return err
	}
	gh := github.New(reg, logger)
	probe := serveFirewallProbe(ctx, cfg.Ingest, logger)
	ctl := controller.New(controller.Deps{Store: db, Recorder: rec, Runtime: rt, GitHub: gh, Logs: logStore, Config: cfg, Capacity: reg.CapacityLimits,
		IngestURL: cfg.Ingest.AdvertiseURL, IngestFingerprint: fingerprint, FirewallProbe: probe})
	tpl := template.NewService(template.Deps{Store: db, Recorder: rec, Logs: logStore, Runtime: rt, Environments: ctl,
		Releases: template.NewGitHubReleases("", "", nil), Config: cfg.Templates, Cache: cfg.Cache, FirewallProbe: probe, BootstrapVMID: p.TemplateVMID, DataDir: cfg.DataDir})
	if err := tpl.EnsureBootstrap(ctx); err != nil {
		return err
	}
	tpl.Recover(ctx)
	ctl.SetTemplates(tpl, tpl)
	mtr := metrics.New(db, ctl, version.Version)
	ctl.SetStages(mtr)
	cacheMon := &cachemon.Monitor{Cache: cfg.Cache, Recorder: rec}
	mtr.SetCache(cacheMon)

	_, _ = rec.Info(ctx, "control_plane.started", "ghrm "+version.Version+" started", events.Refs{},
		map[string]any{"ingest_fingerprint": fingerprint, "scale_sets": len(reg.ScaleSets())})
	logger.Info("starting", "version", version.Version, "listen", cfg.Listen, "ingest", cfg.Ingest.Listen, "ingest_fingerprint", fingerprint)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	// Scale sets come from the file and the UI; listeners follow every change.
	sup := newSupervisor(func(ctx context.Context, ss config.ScaleSet) { listenLoop(ctx, ss, gh, ctl, db, rec, logger) })
	// Removed scale sets keep their listener while they drain (job messages, runner
	// removal); a periodic pass stops it once they are empty.
	applyScaleSets := func() {
		list := reg.ScaleSetConfigs()
		ctl.UpdateScaleSets(list)
		sup.Reconcile(ctx, append(list, ctl.Draining(ctx)...))
	}
	applyScaleSets()
	changes, unsubscribe := reg.Subscribe()
	defer unsubscribe()
	drainTick := time.NewTicker(30 * time.Second)
	defer drainTick.Stop()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-changes:
				applyScaleSets()
			case <-drainTick.C:
				applyScaleSets()
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctl.Run(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		cacheMon.Run(ctx)
	}()
	bk := &backup.Backup{Store: db, Dir: cfg.Backup.Dir, Keep: cfg.Backup.Keep, Hour: cfg.Backup.AtHour(),
		Done: func(path string, err error) {
			if err != nil {
				logger.Error("database backup failed", "error", err)
				_, _ = rec.Error(context.WithoutCancel(ctx), "backup.failed", "database backup failed: "+err.Error(), events.Refs{}, nil)
				return
			}
			_, _ = rec.Info(context.WithoutCancel(ctx), "backup.done", "database copied to "+path, events.Refs{}, map[string]any{"path": path})
		}}
	wg.Add(1)
	go func() {
		defer wg.Done()
		bk.Run(ctx)
	}()
	hist := &retention.Retention{Store: db, Logs: logStore, Recorder: rec, Hour: cfg.Backup.AtHour()}
	wg.Add(1)
	go func() {
		defer wg.Done()
		hist.Run(ctx)
	}()
	if cfg.Templates.Enabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tpl.Run(ctx)
		}()
	}

	ingestSrv := &http.Server{
		Addr:              cfg.Ingest.Listen,
		Handler:           ingest.NewServer(ctl, ctl, logStore, rec, ingest.WithBuilds(tpl)),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Requests (SSE streams in particular) derive from baseCtx, which is cancelled
	// at shutdown so open streams end instead of eating the shutdown budget.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	apiSrv := &http.Server{
		BaseContext: func(net.Listener) context.Context { return baseCtx },
		Addr:        cfg.Listen,
		Handler: api.New(api.Deps{Store: db, Recorder: rec, Logs: logStore, Controller: ctl, AdminToken: cfg.AdminToken, History: hist,
			Config: cfg, Capacity: rt.Capacity, GitHubJobs: gh, UI: uiHandler(), Templates: tpl, Auth: signIn,
			Settings: reg, TestCredential: (&github.REST{}).User, CredentialTargets: (&github.REST{}).Targets, Metrics: mtr.Handler(), Cache: cacheMon,
			Ready: func(ctx context.Context) error { _, err := rt.Capacity(ctx); return err }}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 2)
	go func() { errs <- ignoreClosed(ingestSrv.ListenAndServeTLS("", "")) }()
	go func() { errs <- ignoreClosed(apiSrv.ListenAndServe()) }()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errs:
	}
	logger.Info("shutting down")
	cancel()
	cancelBase()
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	_ = apiSrv.Shutdown(sctx)
	_ = ingestSrv.Shutdown(sctx)
	// Bounded: systemd stops waiting at TimeoutStopSec. Unfinished provisioning is
	// adopted or cleaned up at the next start.
	if !waitOrTimeout(func() { wg.Wait(); sup.Wait(); ctl.Wait(); tpl.Wait() }, 25*time.Second) {
		logger.Warn("shutdown timed out waiting for background work")
	}
	return serveErr
}

// listenLoop keeps a scale set's listener running, restarting it with backoff.
func listenLoop(ctx context.Context, ss config.ScaleSet, gh *github.Client, ctl *controller.Controller, db *store.Store, rec *events.Recorder, logger *slog.Logger) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		id, err := gh.EnsureScaleSet(ctx, ss)
		if err == nil {
			ctl.SetScaleSetID(ss.Name, id)
			_ = db.PutScaleSet(ctx, store.ScaleSetRecord{Name: ss.Name, GitHubID: id, URL: ss.URL})
			ctl.SetListening(ss.Name, true, nil)
			_, _ = rec.Info(ctx, "scaleset.listening", "listening for jobs", events.Refs{ScaleSet: ss.Name}, map[string]any{"github_id": id})
			backoff = 5 * time.Second
			err = gh.Listen(ctx, ss.Name, id, ss.MaxConcurrent, ctl.Scaler(ss.Name))
		}
		if ctx.Err() != nil {
			// Stopped on purpose (a settings change or shutdown): not a failure, and the
			// listener that replaces it reports its own state.
			return
		}
		ctl.SetListening(ss.Name, false, err)
		logger.Warn("scale set listener stopped", "scale_set", ss.Name, "error", err, "retry_in", backoff)
		_, _ = rec.Warn(ctx, "scaleset.listener_error", fmt.Sprintf("listener stopped: %v", err), events.Refs{ScaleSet: ss.Name}, nil)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// waitOrTimeout runs wait and reports whether it returned within d.
func waitOrTimeout(wait func(), d time.Duration) bool {
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// serveFirewallProbe opens the firewall probe next to the ingest port and returns the
// address agents probe (an IP address, so the agent never waits on a resolver), or ""
// when it cannot be served: then guests wait the fixed firewall delay. The probe needs
// the ingest to be advertised on the port it listens on.
func serveFirewallProbe(ctx context.Context, in config.Ingest, logger *slog.Logger) string {
	disabled := func(reason string, args ...any) string {
		logger.Warn("firewall probe disabled: "+reason, args...)
		return ""
	}
	probe, err := ingest.ProbeAddress(in.AdvertiseURL)
	if err != nil {
		return disabled(err.Error())
	}
	probeHost, probePort, _ := net.SplitHostPort(probe)
	listenHost, listenPort, err := net.SplitHostPort(in.Listen)
	if err != nil {
		return disabled("ingest.listen: " + err.Error())
	}
	if p, _ := strconv.Atoi(listenPort); strconv.Itoa(p+1) != probePort {
		return disabled("the ingest listens on another port than it advertises", "listen", in.Listen, "advertise_url", in.AdvertiseURL)
	}
	if _, err := netip.ParseAddr(probeHost); err != nil {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", probeHost)
		if err != nil || len(ips) == 0 {
			return disabled("cannot resolve the ingest host", "host", probeHost, "error", err)
		}
		probe = net.JoinHostPort(ips[0].Unmap().String(), probePort)
	}
	ln, err := ingest.ListenProbe(net.JoinHostPort(listenHost, probePort))
	if err != nil {
		return disabled(err.Error())
	}
	go ingest.ServeProbe(ctx, ln, logger)
	return probe
}
