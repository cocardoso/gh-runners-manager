// Package config loads and validates the ghrm configuration file.
package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// EnvProxmoxTokenSecret, when set and non-empty, overrides proxmox.token_secret_file.
const EnvProxmoxTokenSecret = "GHRM_PROXMOX_TOKEN_SECRET"

// Config is the root of the ghrm configuration file.
type Config struct {
	DataDir        string `yaml:"data_dir"`
	Listen         string `yaml:"listen"`
	AdminTokenFile string `yaml:"admin_token_file"`
	// SecretKeyFile holds the key that seals secrets in the database (default <data_dir>/secret.key).
	SecretKeyFile string     `yaml:"secret_key_file"`
	Proxmox       Proxmox    `yaml:"proxmox"`
	Ingest        Ingest     `yaml:"ingest"`
	GitHub        GitHub     `yaml:"github"`
	Capacity      Capacity   `yaml:"capacity"`
	ScaleSets     []ScaleSet `yaml:"scale_sets"`
	Templates     Templates  `yaml:"templates"`

	// AdminToken is read from AdminTokenFile; empty disables mutating API calls.
	AdminToken string `yaml:"-"`
}

// Ingest configures the HTTPS endpoint agents report to.
type Ingest struct {
	Listen       string `yaml:"listen"`        // bind address on the job network, e.g. 10.50.0.2:8443
	AdvertiseURL string `yaml:"advertise_url"` // URL given to agents, e.g. https://10.50.0.2:8443
}

// GitHub holds the credentials used to talk to GitHub.
type GitHub struct {
	Credentials []Credential `yaml:"credentials"`
}

// Credential is a GitHub token. It is read from TokenFile, or from
// GHRM_GITHUB_TOKEN_<NAME> (upper case, dashes as underscores) when set.
type Credential struct {
	Name      string `yaml:"name"`
	TokenFile string `yaml:"token_file"`
	Token     string `yaml:"-"`
}

// Capacity holds the global limits of spec §9.
type Capacity struct {
	MaxEnvironments int     `yaml:"max_environments"`
	MemoryBudgetMB  int     `yaml:"memory_budget_mb"`
	MemoryMarginMB  int     `yaml:"memory_margin_mb"`
	MaxDiskPercent  float64 `yaml:"max_disk_percent"`
}

// ScaleSet is one GitHub runner scale set served by ghrm.
type ScaleSet struct {
	Name                 string   `yaml:"name"`
	URL                  string   `yaml:"url"` // https://github.com/<owner>[/<repo>]
	Credential           string   `yaml:"credential"`
	RunnerGroup          string   `yaml:"runner_group"`
	Labels               []string `yaml:"labels"`
	MaxConcurrent        int      `yaml:"max_concurrent"`
	Cores                int      `yaml:"cores"`
	MemoryMB             int      `yaml:"memory_mb"`
	KeepOnFailureMinutes int      `yaml:"keep_on_failure_minutes"`
}

// OwnerRepo splits the scale set URL. repo is empty for organization scale sets.
func (s ScaleSet) OwnerRepo() (owner, repo string, err error) {
	m := githubURLPattern.FindStringSubmatch(s.URL)
	if m == nil {
		return "", "", fmt.Errorf("scale set %s: url %q must be https://github.com/<owner>[/<repo>]", s.Name, s.URL)
	}
	return m[1], m[2], nil
}

// Credential returns the named credential.
func (c *Config) Credential(name string) (Credential, bool) {
	for _, cr := range c.GitHub.Credentials {
		if cr.Name == name {
			return cr, true
		}
	}
	return Credential{}, false
}

// Proxmox configures the proxmox-lxc runtime.
type Proxmox struct {
	URL                string    `yaml:"url"`
	Node               string    `yaml:"node"`
	TokenID            string    `yaml:"token_id"`
	TokenSecretFile    string    `yaml:"token_secret_file"`
	InsecureSkipVerify bool      `yaml:"insecure_skip_verify"`
	TLSFingerprint     string    `yaml:"tls_fingerprint"`
	TemplateVMID       int       `yaml:"template_vmid"`
	Pool               string    `yaml:"pool"`
	VMIDRange          VMIDRange `yaml:"vmid_range"`
	ThinPool           string    `yaml:"thin_pool"`
	Storage            string    `yaml:"storage"`
	FirewallSettle     Duration  `yaml:"firewall_settle"`

	// TokenSecret is resolved from EnvProxmoxTokenSecret or TokenSecretFile. It is never read from YAML.
	TokenSecret string `yaml:"-"`
}

// Templates configures template builds (spec §8). Builds are disabled until vmid_range is set.
type Templates struct {
	VMIDRange        VMIDRange `yaml:"vmid_range"`        // VMIDs for built templates, outside proxmox.vmid_range
	Storage          string    `yaml:"storage"`           // storage for template archives ("vztmpl"), default local; prefer a dedicated one
	RootFSGB         int       `yaml:"rootfs_gb"`         // template root disk, default 16
	BuilderDiskGB    int       `yaml:"builder_disk_gb"`   // builder root disk, default 48
	BuilderCores     int       `yaml:"builder_cores"`     // default 4
	BuilderMemoryMB  int       `yaml:"builder_memory_mb"` // default 8192
	Keep             int       `yaml:"keep"`              // versions kept for roll-back, default 2
	CheckIntervalSet *Duration `yaml:"check_interval"`    // release check, default 24h; 0 disables
	AutoActivateSet  *bool     `yaml:"auto_activate"`     // default true
	BuildTimeout     Duration  `yaml:"build_timeout"`     // default 90m
	VerifyTimeout    Duration  `yaml:"verify_timeout"`    // default 20m
	MaxArchiveBytes  int64     `yaml:"max_archive_bytes"` // default 8 GiB
	AgentPath        string    `yaml:"agent_path"`        // ghrm-agent binary served to builders; default: next to ghrm
	Nameserver       string    `yaml:"nameserver"`        // template DNS, default 1.1.1.1
	Bridge           string    `yaml:"bridge"`            // job VNet, default jobnet
	FirewallGroup    string    `yaml:"firewall_group"`    // security group, default gh-runner
	// SelfTestBlocked lists host:port addresses a job must not reach (LAN gateway, hypervisor).
	SelfTestBlocked []string `yaml:"selftest_blocked"`

	// AutoActivate is AutoActivateSet with its default applied.
	AutoActivate bool `yaml:"-"`
	// CheckInterval is CheckIntervalSet with its default applied (0: no automatic checks).
	CheckInterval Duration `yaml:"-"`
}

// Enabled reports whether template builds are configured.
func (t Templates) Enabled() bool { return t.VMIDRange != (VMIDRange{}) }

// VMIDRange is the inclusive range of VMIDs ghrm may allocate for environments.
type VMIDRange struct {
	Start int `yaml:"start"`
	End   int `yaml:"end"`
}

// Duration is a time.Duration written as a Go duration string ("12s", "2m") in YAML.
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Read parses the configuration at path and applies defaults, without validating it or
// resolving secrets (commands that manage the secrets themselves use it).
func Read(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	cfg := &Config{}
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

// Load reads, defaults, resolves secrets for and validates the configuration at path.
func Load(path string) (*Config, error) {
	cfg, err := Read(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.resolveSecrets(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.DataDir == "" {
		c.DataDir = "/var/lib/ghrm"
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.SecretKeyFile == "" {
		c.SecretKeyFile = filepath.Join(c.DataDir, "secret.key")
	}
	cp := &c.Capacity
	if cp.MaxEnvironments == 0 {
		cp.MaxEnvironments = 4
	}
	if cp.MemoryBudgetMB == 0 {
		cp.MemoryBudgetMB = 16384
	}
	if cp.MemoryMarginMB == 0 {
		cp.MemoryMarginMB = 4096
	}
	if cp.MaxDiskPercent == 0 {
		cp.MaxDiskPercent = 85
	}
	for i := range c.ScaleSets {
		ss := &c.ScaleSets[i]
		if ss.RunnerGroup == "" {
			ss.RunnerGroup = "default"
		}
		if ss.MaxConcurrent == 0 {
			ss.MaxConcurrent = 2
		}
		if ss.Cores == 0 {
			ss.Cores = 2
		}
		if ss.MemoryMB == 0 {
			ss.MemoryMB = 4096
		}
	}
	p := &c.Proxmox
	if p.VMIDRange == (VMIDRange{}) {
		p.VMIDRange = VMIDRange{Start: 900, End: 999}
	}
	if p.ThinPool == "" {
		p.ThinPool = "data"
	}
	if p.Storage == "" {
		p.Storage = "local-lvm"
	}
	if p.FirewallSettle == 0 {
		p.FirewallSettle = Duration(12 * time.Second)
	}
	t := &c.Templates
	setDefault := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	setDefault(&t.Storage, "local")
	setDefault(&t.Nameserver, "1.1.1.1")
	setDefault(&t.Bridge, "jobnet")
	setDefault(&t.FirewallGroup, "gh-runner")
	for _, d := range []struct {
		v   *int
		def int
	}{{&t.RootFSGB, 16}, {&t.BuilderDiskGB, 48}, {&t.BuilderCores, 4}, {&t.BuilderMemoryMB, 8192}, {&t.Keep, 2}} {
		if *d.v == 0 {
			*d.v = d.def
		}
	}
	t.CheckInterval = Duration(24 * time.Hour)
	if t.CheckIntervalSet != nil {
		t.CheckInterval = *t.CheckIntervalSet
	}
	if t.BuildTimeout == 0 {
		t.BuildTimeout = Duration(90 * time.Minute)
	}
	if t.VerifyTimeout == 0 {
		t.VerifyTimeout = Duration(20 * time.Minute)
	}
	if t.MaxArchiveBytes == 0 {
		t.MaxArchiveBytes = 8 << 30
	}
	t.AutoActivate = t.AutoActivateSet == nil || *t.AutoActivateSet
}

func (c *Config) resolveSecrets() error {
	for i := range c.GitHub.Credentials {
		cr := &c.GitHub.Credentials[i]
		if v := strings.TrimSpace(os.Getenv(credentialEnv(cr.Name))); v != "" {
			cr.Token = v
			continue
		}
		if cr.TokenFile != "" {
			raw, err := os.ReadFile(cr.TokenFile)
			if err != nil {
				return fmt.Errorf("github credential %s: %w", cr.Name, err)
			}
			cr.Token = strings.TrimSpace(string(raw))
		}
	}
	if c.AdminTokenFile != "" {
		raw, err := os.ReadFile(c.AdminTokenFile)
		if err != nil {
			return fmt.Errorf("admin_token_file: %w", err)
		}
		c.AdminToken = strings.TrimSpace(string(raw))
	}
	p := &c.Proxmox
	if v := strings.TrimSpace(os.Getenv(EnvProxmoxTokenSecret)); v != "" {
		p.TokenSecret = v
		return nil
	}
	if p.TokenSecretFile == "" {
		return nil // the vault may hold it (ResolveVaultSecrets)
	}
	raw, err := os.ReadFile(p.TokenSecretFile)
	if err != nil {
		return fmt.Errorf("proxmox: read token_secret_file: %w", err)
	}
	p.TokenSecret = strings.TrimSpace(string(raw))
	if p.TokenSecret == "" {
		return fmt.Errorf("proxmox: token_secret_file %s is empty", p.TokenSecretFile)
	}
	return nil
}

// Vault names of the secrets the configuration can take from the database.
const (
	VaultProxmoxTokenSecret = "proxmox/token-secret"
	VaultGitHubPrefix       = "github/"
)

// ResolveVaultSecrets fills the secrets that neither an environment variable nor a
// file supplied from the vault (get), which `ghrm secret set` and the UI write.
func (c *Config) ResolveVaultSecrets(ctx context.Context, get func(ctx context.Context, name string) (string, bool, error)) error {
	if c.Proxmox.TokenSecret == "" {
		v, _, err := get(ctx, VaultProxmoxTokenSecret)
		if err != nil {
			return err
		}
		c.Proxmox.TokenSecret = strings.TrimSpace(v)
	}
	for i := range c.GitHub.Credentials {
		cr := &c.GitHub.Credentials[i]
		if cr.Token != "" {
			continue
		}
		v, _, err := get(ctx, VaultGitHubPrefix+cr.Name)
		if err != nil {
			return err
		}
		cr.Token = strings.TrimSpace(v)
	}
	return nil
}

// ValidateSecrets checks, after ResolveVaultSecrets, that every secret has a value.
func (c *Config) ValidateSecrets() error {
	var errs []error
	if c.Proxmox.TokenSecret == "" {
		errs = append(errs, fmt.Errorf("proxmox: token secret missing: set proxmox.token_secret_file, %s, or run \"ghrm secret set %s\"",
			EnvProxmoxTokenSecret, VaultProxmoxTokenSecret))
	}
	for _, cr := range c.GitHub.Credentials {
		if cr.Token == "" {
			errs = append(errs, fmt.Errorf("github credential %s has no token: set token_file, %s, or run \"ghrm secret set %s%s\"",
				cr.Name, credentialEnv(cr.Name), VaultGitHubPrefix, cr.Name))
		}
	}
	return errors.Join(errs...)
}

func credentialEnv(name string) string {
	return "GHRM_GITHUB_TOKEN_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

var (
	fingerprintPattern = regexp.MustCompile(`^[0-9A-Fa-f]{2}(:?[0-9A-Fa-f]{2}){31}$`)
	githubURLPattern   = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9][A-Za-z0-9-]*)(?:/([A-Za-z0-9._-]+))?/?$`)
	scaleSetNameRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// ValidateServe checks the settings that only `ghrm serve` needs.
func (c *Config) ValidateServe() error {
	var errs []error
	if c.Ingest.Listen == "" {
		errs = append(errs, errors.New("ingest.listen is required"))
	}
	if u, err := url.Parse(c.Ingest.AdvertiseURL); c.Ingest.AdvertiseURL == "" || err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("ingest.advertise_url must be an https URL, got %q", c.Ingest.AdvertiseURL))
	}
	creds := map[string]bool{}
	for _, cr := range c.GitHub.Credentials {
		if cr.Name == "" {
			errs = append(errs, errors.New("github.credentials: name is required"))
			continue
		}
		creds[cr.Name] = true
	}
	if len(c.ScaleSets) == 0 {
		errs = append(errs, errors.New("scale_sets: at least one scale set is required"))
	}
	seen := map[string]bool{}
	for _, ss := range c.ScaleSets {
		if !scaleSetNameRe.MatchString(ss.Name) {
			errs = append(errs, fmt.Errorf("scale set name %q must match %s", ss.Name, scaleSetNameRe))
		}
		if seen[ss.Name] {
			errs = append(errs, fmt.Errorf("scale set name %q is duplicated", ss.Name))
		}
		seen[ss.Name] = true
		if _, _, err := ss.OwnerRepo(); err != nil {
			errs = append(errs, err)
		}
		if !creds[ss.Credential] {
			errs = append(errs, fmt.Errorf("scale set %s: unknown credential %q", ss.Name, ss.Credential))
		}
		if ss.MaxConcurrent < 1 || ss.Cores < 1 || ss.MemoryMB < 256 {
			errs = append(errs, fmt.Errorf("scale set %s: max_concurrent, cores and memory_mb must be positive (memory at least 256)", ss.Name))
		}
	}
	return errors.Join(errs...)
}

// Validate checks the configuration after defaults have been applied.
func (c *Config) Validate() error {
	var errs []error
	p := c.Proxmox
	if u, err := url.Parse(p.URL); p.URL == "" || err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, fmt.Errorf("proxmox.url must be an https URL, got %q", p.URL))
	}
	if p.Node == "" {
		errs = append(errs, errors.New("proxmox.node is required"))
	}
	if !strings.Contains(p.TokenID, "!") {
		errs = append(errs, fmt.Errorf("proxmox.token_id must look like user@realm!token, got %q", p.TokenID))
	}
	if p.Pool == "" {
		errs = append(errs, errors.New("proxmox.pool is required: the scoped API token only has permissions inside this resource pool"))
	}
	if p.TLSFingerprint != "" && !fingerprintPattern.MatchString(p.TLSFingerprint) {
		errs = append(errs, fmt.Errorf("proxmox.tls_fingerprint must be a SHA-256 fingerprint (64 hex digits, colons optional), got %q", p.TLSFingerprint))
	}
	if p.TemplateVMID <= 0 {
		errs = append(errs, errors.New("proxmox.template_vmid is required"))
	}
	r := p.VMIDRange
	switch {
	case r.Start < 100 || r.End < r.Start:
		errs = append(errs, fmt.Errorf("proxmox.vmid_range %d-%d is invalid", r.Start, r.End))
	case p.TemplateVMID >= r.Start && p.TemplateVMID <= r.End:
		errs = append(errs, fmt.Errorf("proxmox.template_vmid %d must be outside vmid_range %d-%d", p.TemplateVMID, r.Start, r.End))
	}
	if p.FirewallSettle < 0 {
		errs = append(errs, errors.New("proxmox.firewall_settle must not be negative"))
	}
	if t := c.Templates; t.Enabled() {
		tr := t.VMIDRange
		switch {
		case tr.Start < 100 || tr.End < tr.Start:
			errs = append(errs, fmt.Errorf("templates.vmid_range %d-%d is invalid", tr.Start, tr.End))
		case tr.Start <= r.End && r.Start <= tr.End:
			errs = append(errs, fmt.Errorf("templates.vmid_range %d-%d overlaps proxmox.vmid_range %d-%d", tr.Start, tr.End, r.Start, r.End))
		}
		if t.MaxArchiveBytes <= 0 {
			errs = append(errs, errors.New("templates.max_archive_bytes must be positive"))
		}
		if p.TemplateVMID >= tr.Start && p.TemplateVMID <= tr.End {
			errs = append(errs, fmt.Errorf("proxmox.template_vmid %d (the bootstrap template) must be outside templates.vmid_range %d-%d", p.TemplateVMID, tr.Start, tr.End))
		}
		if t.Keep < 2 {
			errs = append(errs, errors.New("templates.keep must be at least 2 (the active version and one to roll back to)"))
		}
		if t.CheckInterval < 0 || t.BuildTimeout < 0 || t.VerifyTimeout < 0 {
			errs = append(errs, errors.New("templates: durations must not be negative"))
		}
		for _, a := range t.SelfTestBlocked {
			if _, port, err := net.SplitHostPort(a); err != nil || port == "" {
				errs = append(errs, fmt.Errorf("templates.selftest_blocked entry %q must be host:port", a))
			}
		}
	}
	return errors.Join(errs...)
}
