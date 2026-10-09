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
  pool: ghrm
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
	if p.Storage != "local-lvm" {
		t.Errorf("Storage = %q, want local-lvm", p.Storage)
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
		{"missing pool", strings.Replace(validYAML, "  pool: ghrm\n", "", 1), "proxmox.pool"},
		{"bad fingerprint", validYAML + "  tls_fingerprint: nope\n", "tls_fingerprint"},
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

func TestSecretIsRequiredOnceTheVaultIsConsulted(t *testing.T) {
	t.Setenv(EnvProxmoxTokenSecret, "")
	dir := t.TempDir()
	body := strings.Replace(validYAML, "  token_secret_file: %s\n", "", 1)
	cfg, err := Load(writeFile(t, dir, "ghrm.yaml", body))
	if err != nil {
		t.Fatalf("load = %v; the vault may still hold the secret", err)
	}
	if err := cfg.ValidateSecrets(); err == nil || !strings.Contains(err.Error(), "token secret missing") {
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

func TestLoadAcceptsFingerprint(t *testing.T) {
	fp := strings.Repeat("ab:", 31) + "ab"
	cfg, err := load(t, validYAML+"  tls_fingerprint: "+fp+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxmox.TLSFingerprint != fp {
		t.Fatalf("TLSFingerprint = %q", cfg.Proxmox.TLSFingerprint)
	}
}

func TestTemplatesDefaultsAndValidation(t *testing.T) {
	cfg, err := load(t, validYAML+`templates:
  vmid_range: {start: 1950, end: 1958}
  selftest_blocked: ["10.1.1.1:443"]
`)
	if err != nil {
		t.Fatal(err)
	}
	tp := cfg.Templates
	if !tp.Enabled() || tp.Storage != "local" || tp.RootFSGB != 16 || tp.BuilderDiskGB != 48 || tp.BuilderCores != 4 || tp.BuilderMemoryMB != 8192 ||
		tp.Keep != 2 || tp.CheckInterval.Std() != 24*time.Hour || !tp.AutoActivate || tp.BuildTimeout.Std() != 90*time.Minute ||
		tp.VerifyTimeout.Std() != 20*time.Minute || tp.MaxArchiveBytes != 8<<30 || tp.Nameserver != "1.1.1.1" || tp.Bridge != "jobnet" ||
		tp.FirewallGroup != "gh-runner" {
		t.Fatalf("defaults = %+v", tp)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	off, _ := load(t, validYAML)
	if off.Templates.Enabled() {
		t.Fatal("without a vmid_range template builds are disabled")
	}
	for _, bad := range []string{
		"templates:\n  vmid_range: {start: 905, end: 910}\n",                                       // overlaps proxmox.vmid_range 900-999
		"templates:\n  vmid_range: {start: 1950, end: 1958}\n  keep: 1\n",                          // keep >= 2
		"templates:\n  vmid_range: {start: 1950, end: 1958}\n  selftest_blocked: [\"10.1.1.1\"]\n", // host:port
		"templates:\n  vmid_range: {start: 1958, end: 1950}\n",
		"templates:\n  vmid_range: {start: 1950, end: 1958}\n  auto_activate: false\n  check_interval: -1h\n",
	} {
		c, err := load(t, validYAML+bad)
		if err == nil {
			err = c.Validate()
		}
		if err == nil {
			t.Errorf("config %q should be invalid", bad)
		}
	}
	noAuto, _ := load(t, validYAML+"templates:\n  vmid_range: {start: 1950, end: 1958}\n  auto_activate: false\n")
	if noAuto.Templates.AutoActivate {
		t.Fatal("auto_activate: false must be kept")
	}
}

func TestTemplatesCheckIntervalZeroDisables(t *testing.T) {
	cfg, err := load(t, validYAML+"templates:\n  vmid_range: {start: 1950, end: 1958}\n  check_interval: 0s\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Templates.CheckInterval != 0 {
		t.Fatalf("check_interval: 0s = %v, want 0 (disabled)", cfg.Templates.CheckInterval.Std())
	}
}

func TestTemplatesValidationGaps(t *testing.T) {
	for _, bad := range []string{
		"templates:\n  vmid_range: {start: 1950, end: 1958}\n  max_archive_bytes: -1\n",
		strings.Replace(validYAML, "template_vmid: 9000", "template_vmid: 1951", 1) + "templates:\n  vmid_range: {start: 1950, end: 1958}\n",
	} {
		body := bad
		if !strings.Contains(bad, "proxmox:") {
			body = validYAML + bad
		}
		c, err := load(t, body)
		if err == nil {
			err = c.Validate()
		}
		if err == nil {
			t.Errorf("config %q should be invalid", bad)
		}
	}
}

func TestCapacityInFile(t *testing.T) {
	cfg, err := load(t, validYAML)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CapacityInFile {
		t.Error("a file without a capacity section leaves the limits to the UI")
	}
	if cfg.Capacity != (Capacity{MaxEnvironments: 4, MemoryBudgetMB: 16384, MemoryMarginMB: 4096, MaxDiskPercent: 85}) {
		t.Errorf("Capacity = %+v, want the defaults", cfg.Capacity)
	}
	cfg, err = load(t, validYAML+"capacity:\n  memory_budget_mb: 24576\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CapacityInFile || cfg.Capacity.MemoryBudgetMB != 24576 || cfg.Capacity.MaxEnvironments != 4 {
		t.Errorf("CapacityInFile = %v, Capacity = %+v", cfg.CapacityInFile, cfg.Capacity)
	}
	// The section counts even when its values are the zero defaults.
	cfg, err = load(t, validYAML+"capacity:\n  memory_margin_mb: 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CapacityInFile {
		t.Error("a capacity section of zeros is still the file's")
	}
}

func TestCapacityValidate(t *testing.T) {
	ok := Capacity{MaxEnvironments: 4, MemoryBudgetMB: 65536, MemoryMarginMB: 0, MaxDiskPercent: 100}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a budget above the host's memory and no margin are allowed: %v", err)
	}
	for _, c := range []struct {
		cap  Capacity
		want string
	}{
		{Capacity{MaxEnvironments: 0, MemoryBudgetMB: 4096, MaxDiskPercent: 85}, "max_environments"},
		{Capacity{MaxEnvironments: 101, MemoryBudgetMB: 4096, MaxDiskPercent: 85}, "max_environments"},
		{Capacity{MaxEnvironments: 4, MemoryBudgetMB: 256, MaxDiskPercent: 85}, "memory_budget_mb"},
		{Capacity{MaxEnvironments: 4, MemoryBudgetMB: 4096, MemoryMarginMB: -1, MaxDiskPercent: 85}, "memory_margin_mb"},
		{Capacity{MaxEnvironments: 4, MemoryBudgetMB: 4096, MaxDiskPercent: 0.5}, "max_disk_percent"},
		{Capacity{MaxEnvironments: 4, MemoryBudgetMB: 4096, MaxDiskPercent: 101}, "max_disk_percent"},
	} {
		if err := c.cap.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Validate(%+v) = %v, want an error about %s", c.cap, err, c.want)
		}
	}
	if _, err := load(t, validYAML+"capacity:\n  max_disk_percent: 150\n"); err == nil || !strings.Contains(err.Error(), "max_disk_percent") {
		t.Errorf("Load accepted a disk limit above 100%%: %v", err)
	}
}
