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

func TestSecretSetListDelete(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ghrm.yaml")
	// No Proxmox secret yet: this command is how it gets stored.
	_ = os.WriteFile(cfg, []byte("data_dir: "+dir+"\nproxmox:\n  url: https://pve.example:8006\n  node: pve\n  token_id: ghrm@pve!ghrm\n  pool: ghrm\n"), 0o600)
	ctx := context.Background()
	var out, errOut bytes.Buffer
	if code := secretCmd(ctx, []string{"set", "--config", cfg, "proxmox/token-secret"}, strings.NewReader("s3cret-value\n"), &out, &errOut); code != 0 {
		t.Fatalf("set = %d: %s", code, errOut.String())
	}
	out.Reset()
	if code := secretCmd(ctx, []string{"list", "--config", cfg}, nil, &out, &errOut); code != 0 || out.String() != "proxmox/token-secret\n" {
		t.Fatalf("list = %d %q %s", code, out.String(), errOut.String())
	}
	if st, err := os.Stat(filepath.Join(dir, "secret.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", st, err)
	}
	if code := secretCmd(ctx, []string{"set", "--config", cfg, "empty"}, strings.NewReader("\n"), &out, &errOut); code == 0 {
		t.Fatal("an empty value must be refused")
	}
	out.Reset()
	if code := secretCmd(ctx, []string{"delete", "--config", cfg, "proxmox/token-secret"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("delete = %d: %s", code, errOut.String())
	}
	_ = secretCmd(ctx, []string{"list", "--config", cfg}, nil, &out, &errOut)
	if strings.Contains(out.String(), "s3cret") || strings.Contains(out.String(), "proxmox/") {
		t.Fatalf("after delete: %q", out.String())
	}
}

func TestSecretUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := secretCmd(context.Background(), []string{"bogus"}, nil, &out, &errOut); code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestServeTakesSecretsFromTheVault(t *testing.T) {
	t.Setenv("GHRM_PROXMOX_TOKEN_SECRET", "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "ghrm.yaml")
	_ = os.WriteFile(cfg, []byte("data_dir: "+dir+`
listen: 127.0.0.1:0
proxmox:
  url: https://127.0.0.1:1
  node: pve
  token_id: ghrm@pve!ghrm
  template_vmid: 9000
  pool: ghrm
ingest:
  listen: 127.0.0.1:0
  advertise_url: https://127.0.0.1:8443
github:
  credentials:
    - name: personal
scale_sets:
  - name: homelab
    url: https://github.com/octo/repo
    credential: personal
`), 0o600)
	ctx := context.Background()
	var out, errOut bytes.Buffer
	code := run(ctx, []string{"serve", "--config", cfg}, &out, &errOut)
	if code != 1 || !strings.Contains(out.String()+errOut.String(), "ghrm secret set proxmox/token-secret") {
		t.Fatalf("exit %d, output %q %q; want the missing secrets named", code, out.String(), errOut.String())
	}
	_ = secretCmd(ctx, []string{"set", "--config", cfg, "proxmox/token-secret"}, strings.NewReader("x\n"), &out, &errOut)
	_ = secretCmd(ctx, []string{"set", "--config", cfg, "github/personal"}, strings.NewReader("github_pat_x\n"), &out, &errOut)
	out.Reset()
	errOut.Reset()
	// With the secrets in the vault, serve gets past validation (it then runs until
	// its context ends, whatever the unreachable Proxmox host answers).
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = run(tctx, []string{"serve", "--config", cfg}, &out, &errOut)
	if strings.Contains(out.String()+errOut.String(), "secret missing") || strings.Contains(out.String()+errOut.String(), "has no token") {
		t.Fatalf("output %q %q", out.String(), errOut.String())
	}
}
