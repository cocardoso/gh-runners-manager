package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
