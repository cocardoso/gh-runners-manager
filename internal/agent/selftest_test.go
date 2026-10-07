package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSelfTestReportsEveryCheck(t *testing.T) {
	fi := &fakeIngest{}
	c := newBuildClient(t, fi)
	work := t.TempDir()
	cmd := &fakeCommander{
		failOn: "hello-world",
		files: map[string]string{
			"docker compose": filepath.Join(work, "compose", "data", "out") + "|ok\n",
			"pwsh":           filepath.Join(work, "report", "software-report.json") + `|{"NodeType":"HeaderNode","Title":"Ubuntu-Slim"}`,
		},
	}
	opts := SelfTestOptions{
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
