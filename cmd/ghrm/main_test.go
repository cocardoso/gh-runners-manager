package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "ghrm dev") {
		t.Fatalf("stdout = %q, want prefix %q", stdout.String(), "ghrm dev")
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "bogus"`) {
		t.Fatalf("stderr = %q, want unknown command message", stderr.String())
	}
}

func TestRunNoArgsPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage: ghrm") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestSmokeRequiresReadableConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"smoke", "--config", "/nonexistent/ghrm.yaml"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "read config") {
		t.Fatalf("stderr = %q, want config error", stderr.String())
	}
}

func TestSmokeRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"smoke", "--bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestServeRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	_ = os.WriteFile(secret, []byte("s3cret"), 0o600)
	cfg := filepath.Join(dir, "ghrm.yaml")
	_ = os.WriteFile(cfg, []byte("proxmox:\n  url: https://pve.example.test:8006\n  node: pve\n  token_id: a@pve!b\n  token_secret_file: "+secret+"\n  template_vmid: 9000\n  pool: ghrm\n"), 0o600)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"serve", "--config", cfg}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "ingest.listen") {
		t.Fatalf("exit %d, stderr %q; want a serve validation error", code, stderr.String())
	}
}

func TestWaitOrTimeout(t *testing.T) {
	if !waitOrTimeout(func() {}, time.Second) {
		t.Fatal("a quick wait must report completion")
	}
	start := time.Now()
	if waitOrTimeout(func() { time.Sleep(5 * time.Second) }, 50*time.Millisecond) {
		t.Fatal("a slow wait must report a timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatal("waitOrTimeout did not return at its deadline")
	}
}
