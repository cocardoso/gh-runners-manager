package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// load writes a config whose token_secret_file points at a real secret file.
func load(t *testing.T, body string) (*Config, error) {
	t.Helper()
	t.Setenv(EnvProxmoxTokenSecret, "")
	dir := t.TempDir()
	secret := writeFile(t, dir, "secret", "s3cret\n")
	return Load(writeFile(t, dir, "ghrm.yaml", fmt.Sprintf(body, secret)))
}

const validYAML = `proxmox:
  url: https://pve.example.test:8006
  node: pve
  token_id: ghrm@pve!ghrm
  token_secret_file: %s
  template_vmid: 9000
`

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := load(t, validYAML)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Proxmox
	if p.TokenSecret != "s3cret" {
		t.Errorf("TokenSecret = %q, want trimmed file content", p.TokenSecret)
	}
	if p.VMIDRange != (VMIDRange{Start: 900, End: 999}) {
		t.Errorf("VMIDRange = %+v, want 900-999", p.VMIDRange)
	}
	if p.ThinPool != "data" {
		t.Errorf("ThinPool = %q, want data", p.ThinPool)
	}
	if p.FirewallSettle.Std() != 12*time.Second {
		t.Errorf("FirewallSettle = %v, want 12s", p.FirewallSettle.Std())
	}
}

func TestLoadParsesDurationsAndRange(t *testing.T) {
	cfg, err := load(t, validYAML+`  firewall_settle: 20s
  vmid_range:
    start: 800
    end: 850
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxmox.FirewallSettle.Std() != 20*time.Second {
		t.Errorf("FirewallSettle = %v, want 20s", cfg.Proxmox.FirewallSettle.Std())
	}
	if cfg.Proxmox.VMIDRange != (VMIDRange{Start: 800, End: 850}) {
		t.Errorf("VMIDRange = %+v", cfg.Proxmox.VMIDRange)
	}
}

func TestLoadEnvOverridesSecretFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "ghrm.yaml", fmt.Sprintf(validYAML, filepath.Join(dir, "missing")))
	t.Setenv(EnvProxmoxTokenSecret, "from-env")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxmox.TokenSecret != "from-env" {
		t.Errorf("TokenSecret = %q, want from-env", cfg.Proxmox.TokenSecret)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unknown field", validYAML + "  bogus: 1\n", "bogus"},
		{"template inside range", strings.Replace(validYAML, "9000", "950", 1), "outside vmid_range"},
		{"plain http", strings.Replace(validYAML, "https://", "http://", 1), "https"},
		{"token id without bang", strings.Replace(validYAML, "ghrm@pve!ghrm", "ghrm@pve", 1), "token_id"},
		{"bad duration", validYAML + "  firewall_settle: soon\n", "invalid duration"},
		{"missing node", strings.Replace(validYAML, "  node: pve\n", "", 1), "proxmox.node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadRequiresSecret(t *testing.T) {
	t.Setenv(EnvProxmoxTokenSecret, "")
	dir := t.TempDir()
	body := strings.Replace(validYAML, "  token_secret_file: %s\n", "", 1)
	_, err := Load(writeFile(t, dir, "ghrm.yaml", body))
	if err == nil || !strings.Contains(err.Error(), "token secret missing") {
		t.Fatalf("err = %v, want token secret missing", err)
	}
}

func TestLoadRejectsEmptySecretFile(t *testing.T) {
	t.Setenv(EnvProxmoxTokenSecret, "")
	dir := t.TempDir()
	secret := writeFile(t, dir, "secret", "  \n")
	_, err := Load(writeFile(t, dir, "ghrm.yaml", fmt.Sprintf(validYAML, secret)))
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("err = %v, want empty secret error", err)
	}
}
