package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/ids"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/runtime/proxmoxlxc"
)

// smoke creates, starts, inspects and destroys one environment to check the runtime end to end.
func smoke(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "/etc/ghrm/ghrm.yaml", "path to the configuration file")
	cores := fs.Int("cores", 1, "CPU cores for the environment")
	memory := fs.Int("memory", 1024, "memory limit in MB for the environment")
	timeout := fs.Duration("timeout", 3*time.Minute, "overall timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	p := cfg.Proxmox
	client, err := proxmox.New(proxmox.Config{URL: p.URL, TokenID: p.TokenID, TokenSecret: p.TokenSecret, InsecureSkipVerify: p.InsecureSkipVerify, TLSFingerprint: p.TLSFingerprint})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	rt := proxmoxlxc.New(client, proxmoxlxc.Config{
		Node: p.Node, TemplateVMID: p.TemplateVMID, Pool: p.Pool,
		VMIDStart: p.VMIDRange.Start, VMIDEnd: p.VMIDRange.End,
		ThinPool: p.ThinPool, Storage: p.Storage, FirewallSettle: p.FirewallSettle.Std(),
	})

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	start := time.Now()
	report := func(format string, a ...any) {
		fmt.Fprintf(stdout, "[%6.1fs] %s\n", time.Since(start).Seconds(), fmt.Sprintf(format, a...))
	}

	capacity, err := rt.Capacity(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "capacity:", err)
		return 1
	}
	report("host memory %d/%d MB available, thin pool %.1f%% used, %d ghrm environments",
		capacity.HostMemoryAvailableMB, capacity.HostMemoryTotalMB, capacity.ThinPoolPercent, capacity.Environments)

	id := ids.NewEnvironmentID()
	spec := runtime.EnvironmentSpec{
		ID: id, Hostname: "ghrm-smoke-" + id[len(id)-6:], Cores: *cores, MemoryMB: *memory,
		Env: map[string]string{"GHRM_SMOKE": "1", "GHRM_ENVIRONMENT_ID": id},
	}
	ref, err := rt.Create(ctx, spec)
	if err != nil {
		fmt.Fprintln(stderr, "create:", err)
		return 1
	}
	report("created environment %s as %s (includes the firewall settle)", id, ref)

	defer func() {
		dctx, dcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer dcancel()
		if err := rt.Destroy(dctx, ref); err != nil {
			fmt.Fprintln(stderr, "destroy:", err)
			return
		}
		report("destroyed %s", ref)
	}()

	if err := rt.Start(ctx, ref); err != nil {
		fmt.Fprintln(stderr, "start:", err)
		return 1
	}
	report("started %s", ref)

	for {
		st, err := rt.Status(ctx, ref)
		if err == nil && st.Running && st.IP != "" {
			report("running with IP %s (environment id %s)", st.IP, st.EnvironmentID)
			return 0
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "waiting for an IP:", errors.Join(ctx.Err(), err))
			return 1
		case <-time.After(time.Second):
		}
	}
}
