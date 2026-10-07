// Package config loads and validates the ghrm configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// EnvProxmoxTokenSecret, when set and non-empty, overrides proxmox.token_secret_file.
const EnvProxmoxTokenSecret = "GHRM_PROXMOX_TOKEN_SECRET"

// Config is the root of the ghrm configuration file.
type Config struct {
	Proxmox Proxmox `yaml:"proxmox"`
}

// Proxmox configures the proxmox-lxc runtime.
type Proxmox struct {
	URL                string    `yaml:"url"`
	Node               string    `yaml:"node"`
	TokenID            string    `yaml:"token_id"`
	TokenSecretFile    string    `yaml:"token_secret_file"`
	InsecureSkipVerify bool      `yaml:"insecure_skip_verify"`
	TemplateVMID       int       `yaml:"template_vmid"`
	Pool               string    `yaml:"pool"`
	VMIDRange          VMIDRange `yaml:"vmid_range"`
	ThinPool           string    `yaml:"thin_pool"`
	FirewallSettle     Duration  `yaml:"firewall_settle"`

	// TokenSecret is resolved from EnvProxmoxTokenSecret or TokenSecretFile. It is never read from YAML.
	TokenSecret string `yaml:"-"`
}

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

// Load reads, defaults, resolves secrets for and validates the configuration at path.
func Load(path string) (*Config, error) {
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.resolveSecrets(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	p := &c.Proxmox
	if p.VMIDRange == (VMIDRange{}) {
		p.VMIDRange = VMIDRange{Start: 900, End: 999}
	}
	if p.ThinPool == "" {
		p.ThinPool = "data"
	}
	if p.FirewallSettle == 0 {
		p.FirewallSettle = Duration(12 * time.Second)
	}
}

func (c *Config) resolveSecrets() error {
	p := &c.Proxmox
	if v := strings.TrimSpace(os.Getenv(EnvProxmoxTokenSecret)); v != "" {
		p.TokenSecret = v
		return nil
	}
	if p.TokenSecretFile == "" {
		return errors.New("proxmox: token secret missing: set proxmox.token_secret_file or " + EnvProxmoxTokenSecret)
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
	return errors.Join(errs...)
}
