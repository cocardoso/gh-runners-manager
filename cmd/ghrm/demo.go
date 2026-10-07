package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/api"
	"github.com/cocardoso/gh-runners-manager/internal/demo"
)

// demoCmd serves the API (and UI) backed by a simulated fleet.
func demoCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:8080", "address to serve the API and UI on")
	seed := fs.Int64("seed", time.Now().UnixNano(), "random seed")
	tick := fs.Duration("tick", 500*time.Millisecond, "simulation step")
	dataDir := fs.String("data-dir", "", "state directory (default: a temporary directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir := *dataDir
	if dir == "" {
		tmp, err := os.MkdirTemp("", "ghrm-demo-")
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer os.RemoveAll(tmp)
		dir = filepath.Join(tmp, "state")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d, err := demo.New(ctx, demo.Options{DataDir: dir, Seed: *seed, Tick: *tick})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer d.Close()
	go d.Run(ctx)

	srv := &http.Server{
		Addr:              *listen,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 10 * time.Second,
		Handler: api.New(api.Deps{Store: d.Store, Recorder: d.Recorder, Logs: d.Logs, Controller: d.Controller,
			Config: d.Config, Capacity: d.Runtime.Capacity, AdminToken: "demo", UI: uiHandler()}),
	}
	fmt.Fprintf(stdout, "ghrm demo: serving a simulated fleet on http://%s (admin token: demo)\n", *listen)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
	return 0
}
