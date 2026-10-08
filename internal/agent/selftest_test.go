package agent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

func TestRunSelfTestReportsEveryCheck(t *testing.T) {
	fi := &fakeIngest{}
	c := newBuildClient(t, fi)
	work := t.TempDir()
	cmd := &fakeCommander{
		failOn: "hello-world",
		files: map[string]string{
			"docker compose": filepath.Join(work, "compose", "data", "out") + "|ok\n",
			"env INSTALLER_SCRIPT_FOLDER=" + filepath.Join(work, "runner-images", "images", "ubuntu-slim", "toolsets") + " pwsh ": filepath.Join(work, "report", "software-report.json") + `|{"NodeType":"HeaderNode","Title":"Ubuntu-Slim"}`,
		},
	}
	scripts := filepath.Join(work, "scripts-root", "scripts")
	cmd.check = func(line string) error {
		if strings.HasPrefix(line, "cp ") && !strings.Contains(line, " "+scripts+"/") {
			return errors.New("cp target outside the scripts directory: " + line)
		}
		if strings.HasPrefix(line, "cp ") {
			if _, err := os.Stat(scripts); err != nil {
				return errors.New("the scripts directory does not exist")
			}
		}
		if strings.HasPrefix(line, "env INSTALLER_SCRIPT_FOLDER=") {
			if _, err := os.Stat(filepath.Join(work, "report")); err != nil {
				return errors.New("the report output directory does not exist")
			}
		}
		return nil
	}
	opts := SelfTestOptions{
		ScriptsDir:   scripts,
		Work:         work,
		RunnerDir:    "/home/runner/actions-runner",
		BlockedAddrs: []string{"10.1.1.1:443", "10.1.1.6:8006"},
		Lookup:       func(context.Context, string) error { return nil },
		HTTPGet:      func(context.Context, string) error { return nil },
		Dial: func(_ context.Context, addr string) error {
			if addr == "10.1.1.6:8006" {
				return nil // reachable: the check must fail
			}
			return errors.New("i/o timeout")
		},
	}
	rep, err := RunSelfTest(context.Background(), c, cmd, opts)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, ck := range rep.Checks {
		got[ck.Name] = ck.OK
	}
	want := map[string]bool{
		"docker hello-world": false, "buildx docker-container build": true, "compose with a bind mount": true,
		"dns": true, "outbound https": true, "blocked 10.1.1.1:443": true, "blocked 10.1.1.6:8006": false, "runner binary": true,
		"software report": true,
	}
	for name, ok := range want {
		if v, present := got[name]; !present || v != ok {
			t.Errorf("check %q = %v (present %v), want %v", name, v, present, ok)
		}
	}
	if !strings.Contains(string(rep.Software), "Ubuntu-Slim") {
		t.Fatalf("software = %s", rep.Software)
	}
	_ = c.Flush(context.Background())
	fi.mu.Lock()
	defer fi.mu.Unlock()
	if fi.report == nil || len(fi.report.Checks) != len(rep.Checks) {
		t.Fatal("the report was not posted")
	}
}

// A template that points at the cache checks, from a job's point of view, that every
// mirror port answers.
func TestSelfTestChecksTheCache(t *testing.T) {
	c := newBuildClient(t, &fakeIngest{})
	var asked []string
	opts := SelfTestOptions{Work: t.TempDir(), RunnerDir: "/r", Mirrors: []string{"10.50.0.3:5000", "10.50.0.3:5002"},
		Lookup: func(context.Context, string) error { return nil },
		HTTPGet: func(_ context.Context, url string) error {
			asked = append(asked, url)
			if strings.Contains(url, ":5002") {
				return errors.New("connection refused")
			}
			return nil
		},
		Dial: func(context.Context, string) error { return errors.New("blocked") }}
	rep, _ := RunSelfTest(context.Background(), c, &fakeCommander{}, opts)
	got := map[string]ingest.Check{}
	for _, ck := range rep.Checks {
		got[ck.Name] = ck
	}
	if ck := got["registry cache 10.50.0.3:5000"]; !ck.OK {
		t.Errorf("reachable mirror = %+v", ck)
	}
	// Jobs still work without the cache (they pull from the registries), so a cache that
	// is down must not block a template, only show a warning.
	if ck := got["registry cache 10.50.0.3:5002"]; !ck.OK || !ck.Warning || !strings.Contains(ck.Detail, "connection refused") {
		t.Errorf("unreachable mirror = %+v; want a passed check with a warning", ck)
	}
	if !strings.Contains(strings.Join(asked, " "), "http://10.50.0.3:5000/v2/") {
		t.Errorf("asked %v", asked)
	}
}

func TestBootstrapReadsTheMirrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "environ")
	_ = os.WriteFile(p, []byte("GHRM_INGEST_URL=https://x\x00GHRM_INGEST_TOKEN=t\x00GHRM_INGEST_FINGERPRINT=AA\x00GHRM_MODE=selftest\x00GHRM_SELFTEST_MIRRORS=10.50.0.3:5000,10.50.0.3:5001\x00"), 0o600)
	b, ok, err := LoadBootstrap(p)
	if err != nil || !ok || strings.Join(b.Mirrors, ",") != "10.50.0.3:5000,10.50.0.3:5001" {
		t.Fatalf("bootstrap = %+v %v %v", b, ok, err)
	}
}

func TestSelfTestProvesTheFirewallProbeIsDropped(t *testing.T) {
	for name, tc := range map[string]struct {
		dial    error
		warning bool
	}{
		"dropped":  {dial: &net.OpError{Op: "dial", Err: timeoutErr{}}},
		"answered": {dial: nil, warning: true},
		"refused":  {dial: &net.OpError{Op: "dial", Err: &osSyscallErr{syscall.ECONNREFUSED}}, warning: true},
	} {
		c := newBuildClient(t, &fakeIngest{})
		opts := SelfTestOptions{Work: t.TempDir(), RunnerDir: "/r", FirewallProbe: "10.50.0.2:8444",
			Lookup: func(context.Context, string) error { return nil }, HTTPGet: func(context.Context, string) error { return nil },
			Dial: func(context.Context, string) error { return tc.dial }}
		rep, _ := RunSelfTest(context.Background(), c, &fakeCommander{}, opts)
		if len(rep.Features) != 1 || rep.Features[0] != ingest.FeatureFirewallGate {
			t.Fatalf("%s: features = %v", name, rep.Features)
		}
		var ck *ingest.Check
		for i := range rep.Checks {
			if rep.Checks[i].Name == ingest.CheckFirewallProbe {
				ck = &rep.Checks[i]
			}
		}
		// A group that lets the probe through only costs the fast start: a warning, not a failure.
		if ck == nil || !ck.OK || ck.Warning != tc.warning {
			t.Errorf("%s: check = %+v, want ok with warning=%v", name, ck, tc.warning)
		}
	}
}

const reportScript = `$tools = $installedSoftware.AddHeader("Tools")
$tools.AddToolVersion("AzCopy", $(Get-AzCopyVersion))
$cliTools.AddToolVersion("Azure CLI", $(Get-AzureCliVersion))
$cliTools.AddToolVersion("Azure CLI (azure-devops)", $(Get-AzureDevopsVersion))
$packageManagement.AddToolVersion("Pip", $(Get-PipVersion))
`

func TestRemoveReportToolsDropsTheirLines(t *testing.T) {
	got, err := RemoveReportTools(reportScript, []string{"Azure CLI", "Pip"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `"Azure CLI",`) || strings.Contains(got, `"Pip",`) || !strings.Contains(got, `"Azure CLI (azure-devops)",`) || !strings.Contains(got, "AzCopy") {
		t.Fatalf("script = %s", got)
	}
	if _, err := RemoveReportTools(reportScript, []string{"Bicep"}); err == nil || !strings.Contains(err.Error(), "Bicep") {
		t.Fatalf("a tool the script lacks = %v", err)
	}
}
