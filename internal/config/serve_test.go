package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const serveYAML = validYAML + `ingest:
  listen: 10.50.0.2:8443
  advertise_url: https://10.50.0.2:8443
github:
  credentials:
    - name: personal
      token_file: %s
scale_sets:
  - name: homelab
    url: https://github.com/octo/repo
    credential: personal
`

func loadServe(t *testing.T, body string) (*Config, error) {
	t.Helper()
	t.Setenv(EnvProxmoxTokenSecret, "")
	dir := t.TempDir()
	secret := writeFile(t, dir, "secret", "s3cret\n")
	gh := writeFile(t, dir, "gh", "github_pat_x\n")
	cfg, err := Load(writeFile(t, dir, "ghrm.yaml", fmt.Sprintf(body, secret, gh)))
	if err != nil {
		return nil, err
	}
	return cfg, cfg.ValidateServe()
}

func TestServeDefaults(t *testing.T) {
	cfg, err := loadServe(t, serveYAML)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/var/lib/ghrm" || cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("data dir %q listen %q", cfg.DataDir, cfg.Listen)
	}
	c := cfg.Capacity
	if c.MaxEnvironments != 4 || c.MemoryBudgetMB != 16384 || c.MemoryMarginMB != 4096 || c.MaxDiskPercent != 85 {
		t.Errorf("capacity defaults = %+v", c)
	}
	ss := cfg.ScaleSets[0]
	if ss.RunnerGroup != "default" || ss.MaxConcurrent != 2 || ss.Cores != 2 || ss.MemoryMB != 4096 {
		t.Errorf("scale set defaults = %+v", ss)
	}
	if tok := cfg.GitHub.Credentials[0].Token; tok != "github_pat_x" {
		t.Errorf("token = %q", tok)
	}
	owner, repo, err := ss.OwnerRepo()
	if err != nil || owner != "octo" || repo != "repo" {
		t.Errorf("OwnerRepo = %q %q %v", owner, repo, err)
	}
}

func TestServeValidationErrors(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"duplicate name", serveYAML + "  - name: homelab\n    url: https://github.com/octo/other\n    credential: personal\n", "duplicate"},
		{"bad url", strings.Replace(serveYAML, "https://github.com/octo/repo", "https://gitlab.com/octo/repo", 1), "url"},
		{"unknown credential", strings.Replace(serveYAML, "    credential: personal\n", "    credential: other\n", 1), "credential"},
		{"bad name", strings.Replace(serveYAML, "  - name: homelab", "  - name: Home_Lab", 1), "name"},
		{"http advertise", strings.Replace(serveYAML, "advertise_url: https://", "advertise_url: http://", 1), "advertise_url"},
		{"missing ingest", strings.Replace(serveYAML, "  listen: 10.50.0.2:8443\n", "", 1), "ingest.listen"},
		{"above the memory budget", serveYAML + "    memory_mb: 32768\n", "memory_budget_mb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadServe(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestGitHubTokenFromEnv(t *testing.T) {
	t.Setenv(EnvProxmoxTokenSecret, "")
	t.Setenv("GHRM_GITHUB_TOKEN_MY_ORG", "from-env")
	dir := t.TempDir()
	secret := writeFile(t, dir, "secret", "s3cret\n")
	body := strings.Replace(serveYAML, "    - name: personal\n      token_file: %s\n", "    - name: my-org\n", 1)
	body = strings.Replace(body, "credential: personal", "credential: my-org", 1)
	cfg, err := Load(writeFile(t, dir, "ghrm.yaml", fmt.Sprintf(body, secret)))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateServe(); err != nil {
		t.Fatal(err)
	}
	if cfg.GitHub.Credentials[0].Token != "from-env" {
		t.Fatalf("token = %q", cfg.GitHub.Credentials[0].Token)
	}
	_ = filepath.Join
}
