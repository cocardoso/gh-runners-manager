package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/agent"
)

type instanceFlags map[string]string

func (f instanceFlags) String() string { return fmt.Sprint(map[string]string(f)) }
func (f instanceFlags) Set(v string) error {
	origin, cfg, ok := strings.Cut(v, "=")
	if !ok || origin == "" || cfg == "" {
		return fmt.Errorf("--instance wants origin=config, got %q", v)
	}
	f[origin] = cfg
	return nil
}

func cachePruneFromArgs(args []string) (agent.CachePrune, error) {
	fs := flag.NewFlagSet("cache-prune", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "/var/lib/ghrm-cache", "cache directory (one subdirectory per origin)")
	budget := fs.Int64("budget-gb", 100, "disk budget in GB")
	high := fs.Int("high", 85, "prune above this percent of the budget")
	low := fs.Int("low", 70, "until below this percent")
	status := fs.String("status", "", "status file for the exporter (default <root>/status)")
	registry := fs.String("registry", "/usr/local/bin/registry", "the registry binary")
	inst := instanceFlags{}
	fs.Var(inst, "instance", "origin=registry configuration (repeatable)")
	if err := fs.Parse(args); err != nil {
		return agent.CachePrune{}, err
	}
	if *budget <= 0 || *low <= 0 || *high <= *low || *high > 100 {
		return agent.CachePrune{}, errors.New("cache-prune: want budget > 0 and 0 < low < high <= 100")
	}
	if *status == "" {
		*status = filepath.Join(*root, "status")
	}
	return agent.CachePrune{Root: *root, BudgetBytes: *budget << 30, HighPercent: *high, LowPercent: *low, Instances: inst, StatusPath: *status, Registry: *registry}, nil
}

// cacheCommand runs "cache-prune" or "cache-exporter" on the registry cache container.
func cacheCommand(ctx context.Context, name string, args []string) error {
	switch name {
	case "cache-prune":
		p, err := cachePruneFromArgs(args)
		if err != nil {
			return err
		}
		return p.Once(ctx)
	case "cache-exporter":
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		listen := fs.String("listen", ":5199", "address to serve /metrics on")
		status := fs.String("status", "/var/lib/ghrm-cache/status", "status file written by cache-prune")
		if err := fs.Parse(args); err != nil {
			return err
		}
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", agent.CacheExporter(*status))
		srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { <-ctx.Done(); _ = srv.Close() }()
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown command %q", name)
}
