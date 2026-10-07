// Command ghrm-agent runs inside a job environment: it starts the GitHub runner
// from the injected JIT config and streams logs, events and metrics to the
// control plane, then powers the environment off.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/agent"
	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/cocardoso/gh-runners-manager/internal/version"
)

func main() {
	environ := flag.String("environ", "/proc/1/environ", "file holding the bootstrap variables")
	runnerDir := flag.String("runner-dir", "/home/runner/actions-runner", "runner install directory")
	runnerUser := flag.String("runner-user", "runner", "user the runner runs as")
	cgroup := flag.String("cgroup", "/sys/fs/cgroup", "cgroup v2 directory for metrics")
	poweroff := flag.String("poweroff", "systemctl poweroff", "command run after the runner exits")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, *environ, *runnerDir, *runnerUser, *cgroup, *poweroff))
}

func run(ctx context.Context, environ, runnerDir, runnerUser, cgroup, poweroff string) int {
	boot, ok, err := agent.LoadBootstrap(environ)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ok {
		fmt.Println("ghrm-agent: no bootstrap (template boot); idle")
		return 0
	}
	client, err := agent.NewClient(boot, agent.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sendCtx, stopSend := context.WithCancel(context.Background())
	defer stopSend()
	go client.Run(sendCtx)

	host, _ := os.Hostname()
	client.Event(ingest.EventHello, map[string]any{"version": version.Version, "hostname": host, "ip": ipv4(), "mode": boot.Mode})
	client.Log("agent", "ghrm-agent "+version.Version+" started for environment "+boot.EnvironmentID)

	if boot.Mode != "" {
		return runTemplateMode(ctx, client, boot, runnerDir, poweroff, stopSend)
	}

	// The runner writes one log per step and one for the whole job; the job stream
	// carries only the whole-job log, whose record ID is announced in the diag log.
	var jobRecord atomic.Value
	tailer := agent.NewTailer(filepath.Join(runnerDir, "_diag"), map[string]string{
		"Runner_*.log": "runner",
		"Worker_*.log": "runner",
		"pages/*.log":  "job",
	}, func(stream, line string) {
		if id, ok := agent.JobRecordID(line); ok {
			jobRecord.Store(id)
			client.Log("agent", "job record "+id)
		}
		client.Log(stream, line)
	})
	tailer.Accept = func(stream, path string) bool {
		if stream != "job" {
			return true
		}
		id, _ := jobRecord.Load().(string)
		return id != "" && strings.Contains(filepath.Base(path), "_"+id+"_")
	}
	tailCtx, stopTail := context.WithCancel(ctx)
	go tailer.Run(tailCtx, time.Second)
	metricsCtx, stopMetrics := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			if cpu, mem, err := agent.ReadCgroup(cgroup); err == nil {
				client.Metric(cpu, mem)
			}
			select {
			case <-metricsCtx.Done():
				return
			case <-t.C:
			}
		}
	}()

	r := agent.Runner{Dir: runnerDir, Script: filepath.Join(runnerDir, "run.sh"), JIT: boot.JITConfig,
		OnLine: func(line string) {
			client.Log("runner", line)
			if name, data := agent.ClassifyLine(line); name != "" {
				client.Event(name, data)
			}
		}}
	r.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	// Jobs see the image's environment (tool paths, ImageOS, ...), as on hosted runners.
	r.Env = agent.MergeEnvironmentFile(r.Env, "/etc/environment")
	if u, err := user.Lookup(runnerUser); err == nil {
		uid, _ := strconv.ParseUint(u.Uid, 10, 32)
		gid, _ := strconv.ParseUint(u.Gid, 10, 32)
		r.UID, r.GID = uint32(uid), uint32(gid)
		if gids, err := u.GroupIds(); err == nil {
			for _, g := range gids {
				if n, err := strconv.ParseUint(g, 10, 32); err == nil {
					r.Groups = append(r.Groups, uint32(n))
				}
			}
		}
		r.Env = append(r.Env, "HOME="+u.HomeDir, "USER="+u.Username, "LOGNAME="+u.Username)
	} else {
		client.Log("agent", "runner user "+runnerUser+" not found; running as the current user")
	}
	client.Event(ingest.EventRunnerStarted, nil)
	code, err := r.Run(ctx)
	if err != nil {
		client.Log("agent", "runner failed to start: "+err.Error())
	}
	client.Event(ingest.EventRunnerExited, map[string]any{"exit_code": code})

	stopMetrics()
	tailer.Poll() // final read
	stopTail()
	fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_ = client.Flush(fctx)
	cancel()
	client.Event(ingest.EventShutdown, nil)
	fctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	_ = client.Flush(fctx)
	cancel()
	stopSend()

	if poweroff != "" {
		args := strings.Fields(poweroff)
		_ = exec.Command(args[0], args[1:]...).Run() //nolint:gosec // operator-supplied command
	}
	return 0
}

func ipv4() string {
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				return ipn.IP.String()
			}
		}
	}
	return ""
}

// runTemplateMode runs a template build or self-test (spec §8.3, §8.4), then powers off.
func runTemplateMode(ctx context.Context, client *agent.Client, boot agent.Bootstrap, runnerDir, poweroff string, stopSend func()) int {
	code := 0
	cmd := agent.OSCommander{}
	switch boot.Mode {
	case ingest.ModeBuild:
		work := "/var/lib/ghrm-build"
		err := os.MkdirAll(work, 0o755)
		if err == nil {
			err = agent.RunBuild(ctx, client, cmd, work)
		}
		if err != nil {
			client.Log("agent", "build failed: "+err.Error())
			code = 1
		}
	case ingest.ModeSelfTest:
		work := "/var/lib/ghrm-selftest"
		_ = os.MkdirAll(work, 0o755)
		// Checks run in the image's environment, as jobs and GitHub's report tooling see it.
		env := agent.MergeEnvironmentFile(append(os.Environ(), "HOME=/root", "USER=root", "LANG=C.UTF-8"), "/etc/environment")
		rep, err := agent.RunSelfTest(ctx, client, agent.OSCommander{Env: env}, agent.SelfTestOptions{Work: work, RunnerDir: runnerDir, BlockedAddrs: boot.Blocked})
		if err != nil {
			client.Log("agent", "self-test could not report: "+err.Error())
			code = 1
		}
		for _, c := range rep.Checks {
			if !c.OK {
				code = 1
			}
		}
	default:
		client.Log("agent", "unknown mode "+boot.Mode)
		code = 1
	}
	fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_ = client.Flush(fctx)
	cancel()
	client.Event(ingest.EventShutdown, nil)
	fctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	_ = client.Flush(fctx)
	cancel()
	stopSend()
	if poweroff != "" {
		args := strings.Fields(poweroff)
		_ = exec.Command(args[0], args[1:]...).Run() //nolint:gosec // operator-supplied command
	}
	return code
}
