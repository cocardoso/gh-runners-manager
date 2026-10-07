package config

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const vaultYAML = `proxmox:
  url: https://pve.example.test:8006
  node: pve
  token_id: ghrm@pve!ghrm
  template_vmid: 9000
  pool: ghrm
ingest:
  listen: 10.50.0.2:8443
  advertise_url: https://10.50.0.2:8443
github:
  credentials:
    - name: personal
scale_sets:
  - name: homelab
    url: https://github.com/octo/repo
    credential: personal
`

func vault(values map[string]string) func(context.Context, string) (string, bool, error) {
	return func(_ context.Context, name string) (string, bool, error) {
		v, ok := values[name]
		return v, ok, nil
	}
}

func TestVaultSuppliesMissingSecrets(t *testing.T) {
	t.Setenv(EnvProxmoxTokenSecret, "")
	cfg, err := Load(writeFile(t, t.TempDir(), "ghrm.yaml", vaultYAML))
	if err != nil {
		t.Fatalf("a config without secrets must load (the vault may hold them): %v", err)
	}
	if err := cfg.ValidateServe(); err != nil {
		t.Fatal(err)
	}
	err = cfg.ResolveVaultSecrets(context.Background(), vault(map[string]string{
		"proxmox/token-secret": "from-vault", "github/personal": "github_pat_vault"}))
	if err != nil || cfg.Proxmox.TokenSecret != "from-vault" || cfg.GitHub.Credentials[0].Token != "github_pat_vault" {
		t.Fatalf("resolved = %q %q, %v", cfg.Proxmox.TokenSecret, cfg.GitHub.Credentials[0].Token, err)
	}
	if err := cfg.ValidateSecrets(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvAndFileWinOverTheVault(t *testing.T) {
	cfg, err := loadServe(t, serveYAML)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHRM_GITHUB_TOKEN_PERSONAL", "")
	_ = cfg.ResolveVaultSecrets(context.Background(), vault(map[string]string{
		"proxmox/token-secret": "from-vault", "github/personal": "github_pat_vault"}))
	if cfg.Proxmox.TokenSecret != "s3cret" || cfg.GitHub.Credentials[0].Token != "github_pat_x" {
		t.Fatalf("got %q %q; the file values must win", cfg.Proxmox.TokenSecret, cfg.GitHub.Credentials[0].Token)
	}
}

func TestMissingSecretsNameAllSources(t *testing.T) {
	t.Setenv(EnvProxmoxTokenSecret, "")
	cfg, err := Load(writeFile(t, t.TempDir(), "ghrm.yaml", vaultYAML))
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.ResolveVaultSecrets(context.Background(), vault(nil))
	err = cfg.ValidateSecrets()
	msg := fmt.Sprint(err)
	for _, want := range []string{"token_secret_file", EnvProxmoxTokenSecret, "ghrm secret set proxmox/token-secret",
		"GHRM_GITHUB_TOKEN_PERSONAL", "ghrm secret set github/personal"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

func TestBackupDefaults(t *testing.T) {
	cfg, err := loadServe(t, serveYAML)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backup
	if b.Dir != "/var/lib/ghrm/backups" || b.Keep != 7 || b.AtHour() != 3 {
		t.Fatalf("backup defaults = %+v (hour %d)", b, b.AtHour())
	}
	cfg, _ = loadServe(t, serveYAML+"backup:\n  hour: 0\n  keep: 3\n")
	if cfg.Backup.AtHour() != 0 || cfg.Backup.Keep != 3 {
		t.Fatalf("midnight backups = %+v", cfg.Backup)
	}
}
