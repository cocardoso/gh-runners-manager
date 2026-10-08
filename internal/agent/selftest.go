package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// SelfTestOptions configures RunSelfTest (spec §8.4).
type SelfTestOptions struct {
	Work         string
	RunnerDir    string
	BlockedAddrs []string // host:port that must be unreachable (LAN, hypervisor)
	Mirrors      []string // host:port of the registry cache that must be reachable
	// FirewallProbe, when set, must be dropped by the job security group (see WaitFirewall).
	FirewallProbe string
	ProbeURL      string // HTTPS URL that must be reachable (default https://api.github.com)
	// ScriptsDir is where the report scripts are mounted in GitHub's tooling (default /scripts).
	ScriptsDir string
	Lookup     func(ctx context.Context, host string) error
	HTTPGet    func(ctx context.Context, url string) error
	Dial       func(ctx context.Context, addr string) error
}

func (o *SelfTestOptions) defaults() {
	if o.ProbeURL == "" {
		o.ProbeURL = "https://api.github.com"
	}
	if o.ScriptsDir == "" {
		o.ScriptsDir = "/scripts"
	}
	if o.Lookup == nil {
		o.Lookup = func(ctx context.Context, host string) error {
			_, err := net.DefaultResolver.LookupHost(ctx, host)
			return err
		}
	}
	if o.HTTPGet == nil {
		o.HTTPGet = func(ctx context.Context, url string) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			return resp.Body.Close()
		}
	}
	if o.Dial == nil {
		o.Dial = func(ctx context.Context, addr string) error {
			c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return c.Close()
		}
	}
}

// RunSelfTest checks a fresh clone of a template and reports to the control plane.
func RunSelfTest(ctx context.Context, c *Client, cmd Commander, o SelfTestOptions) (ingest.SelfTestReport, error) {
	o.defaults()
	log := func(line string) { c.Log("selftest", line) }
	var rep ingest.SelfTestReport
	// checkWith runs one check; with warnOnly a failure passes with a warning.
	checkWith := func(warnOnly bool, name string, fn func(ctx context.Context) error) {
		log("==> " + name)
		start := time.Now()
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		err := fn(cctx)
		cancel()
		ck := ingest.Check{Name: name, OK: err == nil, Seconds: time.Since(start).Seconds()}
		switch {
		case err == nil:
			log("ok " + name)
		case warnOnly:
			ck.OK, ck.Warning, ck.Detail = true, true, err.Error()
			log("WARN " + name + ": " + err.Error())
		default:
			ck.Detail = err.Error()
			log("FAIL " + name + ": " + err.Error())
		}
		rep.Checks = append(rep.Checks, ck)
	}
	check := func(name string, fn func(ctx context.Context) error) { checkWith(false, name, fn) }
	run := func(dir, name string, args ...string) func(context.Context) error {
		return func(ctx context.Context) error { return cmd.Run(ctx, dir, name, args, log) }
	}

	check("docker hello-world", run(o.Work, "docker", "run", "--rm", "hello-world"))

	bx := filepath.Join(o.Work, "buildx")
	check("buildx docker-container build", func(ctx context.Context) error {
		if err := writeFile(filepath.Join(bx, "Dockerfile"), "FROM busybox\nRUN echo ok > /ok\n"); err != nil {
			return err
		}
		defer func() {
			_ = cmd.Run(context.WithoutCancel(ctx), bx, "docker", []string{"buildx", "rm", "ghrm-selftest"}, log)
		}()
		if err := cmd.Run(ctx, bx, "docker", []string{"buildx", "create", "--name", "ghrm-selftest", "--driver", "docker-container"}, log); err != nil {
			return err
		}
		return cmd.Run(ctx, bx, "docker", []string{"buildx", "build", "--builder", "ghrm-selftest", "--load", "-t", "ghrm-selftest-bx", bx}, log)
	})

	cp := filepath.Join(o.Work, "compose")
	check("compose with a bind mount", func(ctx context.Context) error {
		compose := "services:\n  probe:\n    image: busybox\n    command: [\"sh\", \"-c\", \"echo ok > /data/out\"]\n    volumes:\n      - ./data:/data\n"
		if err := writeFile(filepath.Join(cp, "compose.yaml"), compose); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(cp, "data"), 0o777); err != nil {
			return err
		}
		if err := cmd.Run(ctx, cp, "docker", []string{"compose", "-f", filepath.Join(cp, "compose.yaml"), "up", "--abort-on-container-exit"}, log); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(cp, "data", "out"))
		if err != nil || strings.TrimSpace(string(b)) != "ok" {
			return fmt.Errorf("the container did not write through the bind mount (%v)", err)
		}
		return nil
	})

	check("dns", func(ctx context.Context) error { return o.Lookup(ctx, "github.com") })
	check("outbound https", func(ctx context.Context) error { return o.HTTPGet(ctx, o.ProbeURL) })
	for _, addr := range o.BlockedAddrs {
		check("blocked "+addr, func(ctx context.Context) error {
			dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if err := o.Dial(dctx, addr); err == nil {
				return fmt.Errorf("%s is reachable from a job environment", addr)
			}
			return nil
		})
	}
	rep.Features = []string{ingest.FeatureFirewallGate}
	if o.FirewallProbe != "" {
		// A warning, not a failure: a group that lets the probe through only costs job
		// environments the fast start; the control plane keeps the fixed delay for them.
		checkWith(true, ingest.CheckFirewallProbe, func(ctx context.Context) error {
			dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			err := o.Dial(dctx, o.FirewallProbe)
			if connectTimeout(err) {
				return nil
			}
			if err == nil {
				err = errors.New("connected")
			}
			return fmt.Errorf("the job security group does not drop %s (%v); job environments keep the fixed firewall delay", o.FirewallProbe, err)
		})
	}
	for _, addr := range o.Mirrors {
		// A warning, not a failure: without the cache jobs pull from the registries, and a
		// cache that is down must not hold back a template (a runner update, say).
		checkWith(true, "registry cache "+addr, func(ctx context.Context) error {
			// Any HTTP answer from the registry API root means the mirror is reachable.
			if err := o.HTTPGet(ctx, "http://"+addr+"/v2/"); err != nil {
				return fmt.Errorf("%w; jobs will pull from the registries directly", err)
			}
			return nil
		})
	}
	check("runner binary", run(o.RunnerDir, filepath.Join(o.RunnerDir, "bin", "Runner.Listener"), "--version"))

	check("software report", func(ctx context.Context) error {
		var spec ingest.BuildSpec
		if err := c.GetJSON(ctx, ingest.BuildSpecPath, &spec); err != nil {
			return err
		}
		recipe := filepath.Join(o.Work, "runner-images")
		_ = os.RemoveAll(recipe)
		if err := cmd.Run(ctx, o.Work, "git", []string{"clone", "--depth", "1", "--branch", spec.SlimTag, RunnerImagesURL, recipe}, log); err != nil {
			return err
		}
		// generate-software-report.sh runs this script inside the image with these two mounts.
		if err := os.MkdirAll(o.ScriptsDir, 0o755); err != nil {
			return err
		}
		for src, dst := range map[string]string{
			filepath.Join(recipe, "images", "ubuntu-slim", "scripts", "docs-gen"): filepath.Join(o.ScriptsDir, "docs-gen"),
			filepath.Join(recipe, "helpers", "software-report-base"):              filepath.Join(o.ScriptsDir, "software-report-base"),
		} {
			if err := cmd.Run(ctx, o.Work, "cp", []string{"-a", "-T", src, dst}, log); err != nil {
				return err
			}
		}
		if len(spec.RemoveReport) > 0 {
			script := filepath.Join(o.ScriptsDir, "docs-gen", "Generate-SoftwareReport.ps1")
			b, err := os.ReadFile(script)
			if err != nil {
				return err
			}
			edited, err := RemoveReportTools(string(b), spec.RemoveReport)
			if err != nil {
				return err
			}
			if err := os.WriteFile(script, []byte(edited), 0o644); err != nil {
				return err
			}
		}
		out := filepath.Join(o.Work, "report")
		if err := os.MkdirAll(out, 0o755); err != nil {
			return err
		}
		// The report reads the toolset definitions from INSTALLER_SCRIPT_FOLDER; the image build
		// removes them, so point it at the recipe's copy.
		toolsets := filepath.Join(recipe, "images", "ubuntu-slim", "toolsets")
		if err := cmd.Run(ctx, o.Work, "env", []string{"INSTALLER_SCRIPT_FOLDER=" + toolsets, "pwsh",
			filepath.Join(o.ScriptsDir, "docs-gen", "Generate-SoftwareReport.ps1"), "-OutputDirectory", out}, log); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(out, "software-report.json"))
		if err != nil {
			return err
		}
		if !json.Valid(b) {
			return fmt.Errorf("software-report.json is not valid JSON")
		}
		rep.Software = b
		return nil
	})

	if err := sendReport(ctx, c, rep); err != nil {
		return rep, err
	}
	c.Event(ingest.EventSelfTestFinished, map[string]any{"checks": len(rep.Checks)})
	return rep, nil
}

func sendReport(ctx context.Context, c *Client, rep ingest.SelfTestReport) error {
	b, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	return c.Send(ctx, "POST", ingest.SelfTestPath, strings.NewReader(string(b)), int64(len(b)), map[string]string{"Content-Type": "application/json"})
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// RemoveReportTools drops the tools a template profile leaves out from GitHub's report
// script: it runs every version command under "stop on error", so a missing tool would
// abort the whole report. Each tool is one line: <header>.AddToolVersion("<name>", ...).
// A name the script does not report is an error: the recipe changed under the profile.
func RemoveReportTools(script string, names []string) (string, error) {
	lines := strings.Split(script, "\n")
	for _, name := range names {
		call := `.AddToolVersion("` + name + `",`
		found := false
		kept := lines[:0:0]
		for _, l := range lines {
			if strings.Contains(l, call) {
				found = true
				continue
			}
			kept = append(kept, l)
		}
		if !found {
			return "", fmt.Errorf("the software report does not list %q", name)
		}
		lines = kept
	}
	return strings.Join(lines, "\n"), nil
}
