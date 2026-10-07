# M1 — Foundation and Proxmox Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Go foundation of gh-runners-manager. M1 is complete when `ghrm smoke` can create, start, inspect and destroy a real job environment (an LXC linked clone) on Proxmox through a scoped API token.

**Architecture:** M1 builds a single Go module with the `ghrm` binary and four internal packages: pure domain logic (environment state machine, capacity scheduler), a `runtime.Runtime` interface with an in-memory fake, a thin Proxmox API client with an in-process fake server, and the `proxmox-lxc` runtime. Nothing in M1 talks to GitHub or persists state. Those parts come in M2.

**Tech Stack:** Go 1.27 (standard library HTTP and testing), `go.yaml.in/yaml/v3`, `github.com/oklog/ulid/v2`, GitHub Actions CI.

**Spec:** `docs/superpowers/specs/2026-10-07-gh-runners-manager-design.md`

## Milestone roadmap

Each milestone gets its own plan, written when the previous one is done.

| Milestone | Delivers |
|---|---|
| **M1 (this plan)** | Repository scaffold and CI, configuration, environment state machine, scheduler, runtime interface and fake, Proxmox client, `proxmox-lxc` runtime, and the `ghrm smoke` command verified on a real host with a scoped token (spec risk #2). |
| M2 | SQLite store and events, `actions/scaleset` listener, controller, `ghrm-agent`, ingest, log store, reaper, and the REST/SSE API. Real jobs run end-to-end without a UI. |
| M3 | Web UI (React, Kumo, real-time). |
| M4 | Template builder (`ubuntu-slim` and the ghrm layer), verification and rollout. |
| M5 | UI authentication, encrypted secrets, agent TLS pinning, `install.sh`, Docker Compose and metrics. |

## Global Constraints

- Module path: `github.com/cocardoso/gh-runners-manager`. Go directive: `go 1.27.0`.
- Everything in English: code, comments, identifiers, docs, commit messages.
- Commits are authored by the repository owner only. Never add `Co-Authored-By` trailers or "Generated with" lines.
- `CGO_ENABLED=0 go build ./...` must succeed. No cgo dependencies.
- Tests use the standard `testing` package only (no assertion libraries).
- The Proxmox client authenticates only with API tokens (`PVEAPIToken=<user>@<realm>!<token>=<secret>`). No password or ticket authentication.
- Default VMID range: `900–999`. The template VMID must be outside the range.
- Every ghrm environment carries the tags `ghrm-env` and `ghrmid-<environment id>`.
- Default firewall settle delay: `12s` (spec §10.3).
- Environment state timeouts (spec §5): provisioning 2 min, booting 2 min, connected 2 min, idle 10 min, running 6 h, completing 5 min.
- Do not put real infrastructure details (IP addresses, host names, private repository names) in any committed file. Use `example.test` hosts and documentation addresses.

## Review Focus

1. **A VMID in the range is used by a guest the scoped token cannot see** (outside the `ghrm` pool). Allocation must still skip it. Covered by Task 7 (`TestCreateSkipsVMIDsTakenOutsidePool`).
2. **Destroying or stopping an environment that is already gone** must succeed quietly, because the reaper and the controller will both try it. Covered by Task 7 (`TestDestroyMissingIsNoop`) and Task 6 (`TestDeleteMissingIsNotFound`).
3. **A failure after the clone exists** (config update fails, or the context is cancelled during the firewall settle) must not leak a guest. Covered by Task 7 (`TestCreateCleansUpWhenConfigFails`, `TestCreateCleansUpWhenCancelledDuringSettle`).
4. **Unsafe environment variables** (newline or NUL in values, lowercase or empty keys) must be rejected before any API call, because they would corrupt the LXC `env` option. Covered by Task 5 (`TestSpecValidate`) and Task 7 (`TestCreateRejectsInvalidSpecWithoutAPICalls`).
5. **The VMID range is exhausted.** The runtime must return a clear `ErrNoFreeVMID` and must not clone over an existing guest. Covered by Task 7 (`TestCreateFailsWhenRangeExhausted`).

---

### Task 1: Repository scaffold, `ghrm version` and CI

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `Makefile`
- Create: `README.md`
- Create: `.github/workflows/ci.yml`
- Create: `internal/version/version.go`
- Create: `cmd/ghrm/main.go`
- Test: `cmd/ghrm/main_test.go`

**Interfaces:**
- Produces: `func run(ctx context.Context, args []string, stdout, stderr io.Writer) int` in `cmd/ghrm` (later tasks add commands to its `switch`); `version.String() string`.

- [ ] **Step 1: Initialize the module**

```bash
cd ~/Workspace/pocs/gh-runners-manager
go mod init github.com/cocardoso/gh-runners-manager
go mod edit -go=1.27.0
```

- [ ] **Step 2: Write the failing test**

`cmd/ghrm/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./cmd/ghrm/`
Expected: FAIL to build with `undefined: run`.

- [ ] **Step 4: Write the implementation**

`internal/version/version.go`:

```go
// Package version holds build metadata injected with -ldflags.
package version

import "fmt"

// Version and Commit are overridden at build time, for example:
//
//	-X github.com/cocardoso/gh-runners-manager/internal/version.Version=v0.1.0
var (
	Version = "dev"
	Commit  = "none"
)

// String returns a human-readable version line.
func String() string {
	return fmt.Sprintf("ghrm %s (%s)", Version, Commit)
}
```

`cmd/ghrm/main.go`:

```go
// Command ghrm is the gh-runners-manager control plane.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/cocardoso/gh-runners-manager/internal/version"
)

const usage = `Usage: ghrm <command> [flags]

Commands:
  version   Print version information
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version.String())
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/ghrm/`
Expected: PASS.

- [ ] **Step 6: Add the build tooling, README and CI**

`.gitignore`:

```gitignore
/bin/
/dist/
*.test
*.out
/ghrm.yaml
```

`Makefile` (recipe lines are indented with a tab):

```makefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
PKG     := github.com/cocardoso/gh-runners-manager
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)

.PHONY: build test vet check

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ghrm ./cmd/ghrm

test:
	go test -race ./...

vet:
	go vet ./...

check: vet test build
```

`README.md`:

```markdown
# gh-runners-manager

Ephemeral, isolated GitHub Actions runners on Proxmox LXC: one fresh environment per job, destroyed when the job ends, with a real-time web UI.

> **Status:** early development. Not ready for production use.

## How it works

- Jobs are received through GitHub's Runner Scale Set API (outbound connections only).
- Each job runs in an unprivileged LXC container, linked-cloned from a template that is built from GitHub's own `ubuntu-slim` image recipe.
- Job environments live on an isolated network that cannot reach your LAN or the hypervisor.
- A web UI shows queues, environments, jobs and the live logs of every lifecycle stage.

Read the [design document](docs/superpowers/specs/2026-10-07-gh-runners-manager-design.md) for details.

## Development

Requirements: Go 1.27+.

    make check   # vet, test (with -race) and build
    make build   # produces bin/ghrm
```

`.github/workflows/ci.yml`:

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race ./...
      - run: CGO_ENABLED=0 go build ./...
```

- [ ] **Step 7: Verify the whole check passes**

Run: `make check`
Expected: vet prints nothing, tests PASS, `bin/ghrm` is created. `./bin/ghrm version` prints `ghrm <git describe> (<sha>)`.

- [ ] **Step 8: Commit**

```bash
git add go.mod .gitignore Makefile README.md .github internal/version cmd/ghrm
git commit -m "chore: scaffold module, ghrm version command and CI"
```

---

### Task 2: Configuration loading

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.Load(path string) (*config.Config, error)`
  - `config.Config{Proxmox config.Proxmox}`
  - `config.Proxmox{URL, Node, TokenID, TokenSecretFile string; InsecureSkipVerify bool; TemplateVMID int; Pool string; VMIDRange config.VMIDRange; ThinPool string; FirewallSettle config.Duration; TokenSecret string}`
  - `config.VMIDRange{Start, End int}`
  - `config.Duration` with `Std() time.Duration`
  - `config.EnvProxmoxTokenSecret = "GHRM_PROXMOX_TOKEN_SECRET"`

- [ ] **Step 1: Add the YAML dependency**

Run: `go get go.yaml.in/yaml/v3@latest`

- [ ] **Step 2: Write the failing tests**

`internal/config/config_test.go`:

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/config/`
Expected: FAIL to build with `undefined: Load`.

- [ ] **Step 4: Write the implementation**

`internal/config/config.go`:

```go
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config
git commit -m "feat(config): load and validate the Proxmox runtime configuration"
```

---

### Task 3: Environment state machine

**Files:**
- Create: `internal/environment/state.go`
- Test: `internal/environment/state_test.go`

**Interfaces:**
- Produces:
  - `environment.State` with constants `Pending, Provisioning, Booting, Connected, Idle, Running, Completing, Destroying, Destroyed, Failed`
  - `(State) Valid() bool`, `(State) Live() bool`
  - `environment.CanTransition(from, to State) bool`
  - `environment.Timeouts` (`map[State]time.Duration`) with `(Timeouts) Expired(state State, since, now time.Time) bool`
  - `environment.DefaultTimeouts() Timeouts`

- [ ] **Step 1: Write the failing tests**

`internal/environment/state_test.go`:

```go
package environment

import (
	"testing"
	"time"
)

func TestTransitions(t *testing.T) {
	allowed := []struct{ from, to State }{
		{Pending, Provisioning},
		{Provisioning, Booting},
		{Booting, Connected},
		{Connected, Idle},
		{Idle, Running},
		{Idle, Completing},
		{Running, Completing},
		{Completing, Destroying},
		{Destroying, Destroyed},
		{Running, Failed},
		{Failed, Destroying},
		{Booting, Destroying},
	}
	for _, tc := range allowed {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s, %s) = false, want true", tc.from, tc.to)
		}
	}
	denied := []struct{ from, to State }{
		{Pending, Running},
		{Destroyed, Pending},
		{Destroyed, Destroying},
		{Failed, Running},
		{Running, Idle},
		{Completing, Running},
		{Destroying, Failed},
		{State("bogus"), Pending},
	}
	for _, tc := range denied {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s, %s) = true, want false", tc.from, tc.to)
		}
	}
}

func TestEveryTargetIsAValidState(t *testing.T) {
	for from, targets := range transitions {
		for _, to := range targets {
			if !to.Valid() {
				t.Errorf("%s -> %s targets an unknown state", from, to)
			}
		}
	}
}

func TestLive(t *testing.T) {
	if Destroyed.Live() {
		t.Error("Destroyed.Live() = true")
	}
	if !Failed.Live() || !Running.Live() || !Pending.Live() {
		t.Error("non-destroyed states must be live")
	}
	if State("bogus").Live() {
		t.Error("unknown state must not be live")
	}
}

func TestTimeoutsExpired(t *testing.T) {
	tt := DefaultTimeouts()
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if tt.Expired(Provisioning, since, since.Add(2*time.Minute)) {
		t.Error("exactly at the timeout must not be expired")
	}
	if !tt.Expired(Provisioning, since, since.Add(2*time.Minute+time.Nanosecond)) {
		t.Error("past the timeout must be expired")
	}
	if tt.Expired(Pending, since, since.Add(1000*time.Hour)) {
		t.Error("states without a timeout never expire")
	}
	want := map[State]time.Duration{
		Provisioning: 2 * time.Minute, Booting: 2 * time.Minute, Connected: 2 * time.Minute,
		Idle: 10 * time.Minute, Running: 6 * time.Hour, Completing: 5 * time.Minute,
	}
	for s, d := range want {
		if tt[s] != d {
			t.Errorf("DefaultTimeouts()[%s] = %v, want %v", s, tt[s], d)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/environment/`
Expected: FAIL to build with `undefined: CanTransition`.

- [ ] **Step 3: Write the implementation**

`internal/environment/state.go`:

```go
// Package environment defines the lifecycle of a job environment.
package environment

import "time"

// State is a lifecycle state of a job environment (spec §5).
type State string

const (
	Pending      State = "pending"
	Provisioning State = "provisioning"
	Booting      State = "booting"
	Connected    State = "connected"
	Idle         State = "idle"
	Running      State = "running"
	Completing   State = "completing"
	Destroying   State = "destroying"
	Destroyed    State = "destroyed"
	Failed       State = "failed"
)

var transitions = map[State][]State{
	Pending:      {Provisioning, Failed, Destroying},
	Provisioning: {Booting, Failed, Destroying},
	Booting:      {Connected, Failed, Destroying},
	Connected:    {Idle, Failed, Destroying},
	Idle:         {Running, Completing, Failed, Destroying},
	Running:      {Completing, Failed, Destroying},
	Completing:   {Destroying, Failed},
	Failed:       {Destroying},
	Destroying:   {Destroyed},
	Destroyed:    nil,
}

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// Live reports whether an environment in state s still exists or may exist.
func (s State) Live() bool { return s.Valid() && s != Destroyed }

// CanTransition reports whether an environment may move from one state to another.
func CanTransition(from, to State) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Timeouts bounds how long an environment may stay in a state. States without an entry never time out.
type Timeouts map[State]time.Duration

// DefaultTimeouts returns the timeouts from spec §5.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Provisioning: 2 * time.Minute,
		Booting:      2 * time.Minute,
		Connected:    2 * time.Minute,
		Idle:         10 * time.Minute,
		Running:      6 * time.Hour,
		Completing:   5 * time.Minute,
	}
}

// Expired reports whether an environment that entered state at since has outlived its timeout at now.
func (t Timeouts) Expired(state State, since, now time.Time) bool {
	d, ok := t[state]
	return ok && now.Sub(since) > d
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/environment/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/environment
git commit -m "feat(environment): add lifecycle states, transitions and timeouts"
```

---

### Task 4: Capacity scheduler

**Files:**
- Create: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`

**Interfaces:**
- Produces:
  - `scheduler.Demand{ScaleSet string; Desired, Live, MaxConcurrent, MemoryMB int; WaitingSince time.Time}`
  - `scheduler.Capacity{MaxEnvironments, LiveEnvironments, MemoryBudgetMB, CommittedMemoryMB, HostAvailableMB, MemoryMarginMB int; ThinPoolPercent, MaxThinPoolPercent float64}`
  - `scheduler.Reason` with `ReasonScaleSetLimit, ReasonGlobalLimit, ReasonMemoryBudget, ReasonHostMemory, ReasonDisk`
  - `scheduler.Plan{Create map[string]int; Waiting map[string]Reason}`
  - `scheduler.Decide(demands []Demand, c Capacity) Plan`

Ordering rule (this refines the spec's "first-in, first-out across scale sets"): the scale set with the oldest `WaitingSince` is served first, then environments are handed out one at a time in that order (round-robin), so one large backlog cannot starve the others.

- [ ] **Step 1: Write the failing tests**

`internal/scheduler/scheduler_test.go`:

```go
package scheduler

import (
	"testing"
	"time"
)

func roomy() Capacity {
	return Capacity{
		MaxEnvironments: 10, MemoryBudgetMB: 64 * 1024, HostAvailableMB: 64 * 1024,
		MemoryMarginMB: 4096, MaxThinPoolPercent: 85,
	}
}

func demand(name string, desired, live int) Demand {
	return Demand{ScaleSet: name, Desired: desired, Live: live, MaxConcurrent: 4, MemoryMB: 4096}
}

func TestCreatesUpToDesired(t *testing.T) {
	p := Decide([]Demand{demand("a", 2, 0)}, roomy())
	if p.Create["a"] != 2 || len(p.Waiting) != 0 {
		t.Fatalf("plan = %+v, want create 2 and nothing waiting", p)
	}
}

func TestAccountsForLiveEnvironments(t *testing.T) {
	p := Decide([]Demand{demand("a", 3, 2)}, roomy())
	if p.Create["a"] != 1 {
		t.Fatalf("create = %d, want 1", p.Create["a"])
	}
	p = Decide([]Demand{demand("a", 1, 3)}, roomy())
	if p.Create["a"] != 0 || len(p.Waiting) != 0 {
		t.Fatalf("plan = %+v, want nothing when live exceeds desired", p)
	}
}

func TestNothingToDo(t *testing.T) {
	p := Decide([]Demand{demand("a", 0, 0)}, roomy())
	if len(p.Create) != 0 || len(p.Waiting) != 0 {
		t.Fatalf("plan = %+v, want empty", p)
	}
}

func TestScaleSetLimit(t *testing.T) {
	d := demand("a", 5, 0)
	d.MaxConcurrent = 2
	p := Decide([]Demand{d}, roomy())
	if p.Create["a"] != 2 || p.Waiting["a"] != ReasonScaleSetLimit {
		t.Fatalf("plan = %+v, want create 2 and waiting scale_set_limit", p)
	}
}

func TestGlobalLimit(t *testing.T) {
	c := roomy()
	c.MaxEnvironments, c.LiveEnvironments = 3, 2
	p := Decide([]Demand{demand("a", 2, 0)}, c)
	if p.Create["a"] != 1 || p.Waiting["a"] != ReasonGlobalLimit {
		t.Fatalf("plan = %+v, want create 1 and waiting global_limit", p)
	}
}

func TestMemoryBudget(t *testing.T) {
	c := roomy()
	c.MemoryBudgetMB, c.CommittedMemoryMB = 8192, 4096
	d := demand("a", 1, 0)
	d.MemoryMB = 6144
	p := Decide([]Demand{d}, c)
	if p.Create["a"] != 0 || p.Waiting["a"] != ReasonMemoryBudget {
		t.Fatalf("plan = %+v, want waiting memory_budget", p)
	}
}

func TestHostMemoryMargin(t *testing.T) {
	c := roomy()
	c.HostAvailableMB = 9000
	d := demand("a", 1, 0)
	d.MemoryMB = 6144
	p := Decide([]Demand{d}, c)
	if p.Create["a"] != 0 || p.Waiting["a"] != ReasonHostMemory {
		t.Fatalf("plan = %+v, want waiting host_memory", p)
	}
}

func TestDisk(t *testing.T) {
	c := roomy()
	c.ThinPoolPercent = 90
	p := Decide([]Demand{demand("a", 1, 0)}, c)
	if p.Create["a"] != 0 || p.Waiting["a"] != ReasonDisk {
		t.Fatalf("plan = %+v, want waiting disk", p)
	}
	c.MaxThinPoolPercent = 0 // disabled
	if p := Decide([]Demand{demand("a", 1, 0)}, c); p.Create["a"] != 1 {
		t.Fatalf("disk check must be disabled when MaxThinPoolPercent is 0, plan = %+v", p)
	}
}

func TestOldestFirstThenRoundRobin(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	older, newer := demand("newer-name-a", 2, 0), demand("z-older", 2, 0)
	older.WaitingSince, newer.WaitingSince = t0.Add(time.Minute), t0
	c := roomy()
	c.MaxEnvironments = 1
	p := Decide([]Demand{older, newer}, c)
	if p.Create["z-older"] != 1 || p.Create["newer-name-a"] != 0 {
		t.Fatalf("plan = %+v, want the oldest waiting scale set served first", p)
	}
	c.MaxEnvironments = 2
	p = Decide([]Demand{older, newer}, c)
	if p.Create["z-older"] != 1 || p.Create["newer-name-a"] != 1 {
		t.Fatalf("plan = %+v, want one each (round-robin)", p)
	}
}

func TestDemandWithoutWaitingSinceGoesLast(t *testing.T) {
	waiting := demand("b", 1, 0)
	waiting.WaitingSince = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := roomy()
	c.MaxEnvironments = 1
	p := Decide([]Demand{demand("a", 1, 0), waiting}, c)
	if p.Create["b"] != 1 {
		t.Fatalf("plan = %+v, want the scale set with a waiting time first", p)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/scheduler/`
Expected: FAIL to build with `undefined: Decide`.

- [ ] **Step 3: Write the implementation**

`internal/scheduler/scheduler.go`:

```go
// Package scheduler decides how many environments to create for each scale set
// under the global capacity limits (spec §9). It is pure: no I/O and no clock.
package scheduler

import (
	"sort"
	"time"
)

// Demand describes what one scale set wants right now.
type Demand struct {
	ScaleSet      string
	Desired       int       // assigned jobs plus warm pool
	Live          int       // environments that exist and are not destroyed
	MaxConcurrent int       // per-scale-set limit
	MemoryMB      int       // memory limit of one environment
	WaitingSince  time.Time // when the oldest unserved demand appeared; zero if unknown
}

// Capacity is a snapshot of global limits and current usage.
type Capacity struct {
	MaxEnvironments    int
	LiveEnvironments   int
	MemoryBudgetMB     int
	CommittedMemoryMB  int // sum of memory limits of live environments
	HostAvailableMB    int
	MemoryMarginMB     int
	ThinPoolPercent    float64
	MaxThinPoolPercent float64 // 0 disables the disk check
}

// Reason explains why a scale set has demand that is not being served.
type Reason string

const (
	ReasonScaleSetLimit Reason = "scale_set_limit"
	ReasonGlobalLimit   Reason = "global_limit"
	ReasonMemoryBudget  Reason = "memory_budget"
	ReasonHostMemory    Reason = "host_memory"
	ReasonDisk          Reason = "disk"
)

// Plan is the scheduler's decision.
type Plan struct {
	Create  map[string]int    // environments to create per scale set
	Waiting map[string]Reason // why a scale set still has unserved demand
}

// Decide returns how many environments to create per scale set. The scale set
// that has waited longest is served first, then environments are handed out
// one at a time in that order.
func Decide(demands []Demand, c Capacity) Plan {
	plan := Plan{Create: map[string]int{}, Waiting: map[string]Reason{}}

	ordered := append([]Demand(nil), demands...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].WaitingSince, ordered[j].WaitingSince
		switch {
		case a.Equal(b):
			return ordered[i].ScaleSet < ordered[j].ScaleSet
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		default:
			return a.Before(b)
		}
	})

	remaining := make([]int, len(ordered))
	for i, d := range ordered {
		if n := min(d.Desired, d.MaxConcurrent) - d.Live; n > 0 {
			remaining[i] = n
		}
	}

	for progress := true; progress; {
		progress = false
		for i, d := range ordered {
			if remaining[i] == 0 {
				continue
			}
			if reason, ok := blocked(d, c); ok {
				plan.Waiting[d.ScaleSet] = reason
				remaining[i] = 0 // capacity only shrinks within one decision
				continue
			}
			plan.Create[d.ScaleSet]++
			remaining[i]--
			c.LiveEnvironments++
			c.CommittedMemoryMB += d.MemoryMB
			c.HostAvailableMB -= d.MemoryMB
			progress = true
		}
	}

	for _, d := range ordered {
		if _, waiting := plan.Waiting[d.ScaleSet]; !waiting && d.Desired > d.MaxConcurrent {
			plan.Waiting[d.ScaleSet] = ReasonScaleSetLimit
		}
	}
	return plan
}

func blocked(d Demand, c Capacity) (Reason, bool) {
	switch {
	case c.MaxThinPoolPercent > 0 && c.ThinPoolPercent >= c.MaxThinPoolPercent:
		return ReasonDisk, true
	case c.LiveEnvironments >= c.MaxEnvironments:
		return ReasonGlobalLimit, true
	case c.CommittedMemoryMB+d.MemoryMB > c.MemoryBudgetMB:
		return ReasonMemoryBudget, true
	case c.HostAvailableMB-d.MemoryMB < c.MemoryMarginMB:
		return ReasonHostMemory, true
	}
	return "", false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/scheduler/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler
git commit -m "feat(scheduler): decide environment creation under capacity limits"
```

---

### Task 5: Runtime interface and in-memory fake

**Files:**
- Create: `internal/runtime/runtime.go`
- Create: `internal/runtime/runtimetest/fake.go`
- Test: `internal/runtime/runtime_test.go`
- Test: `internal/runtime/runtimetest/fake_test.go`

**Interfaces:**
- Produces:
  - `runtime.Runtime` interface: `Create`, `Start`, `Stop`, `Destroy`, `List`, `Status`, `Capacity`
  - `runtime.EnvironmentSpec{ID, Hostname string; Cores, MemoryMB int; Env map[string]string}` with `Validate() error`
  - `runtime.Ref{ID string}`
  - `runtime.Status{Ref Ref; EnvironmentID string; Running bool; IP string}`
  - `runtime.Capacity{HostMemoryTotalMB, HostMemoryAvailableMB int; ThinPoolPercent float64; Environments int}`
  - `runtime.ErrNotFound`, `runtime.ErrInvalidSpec`
  - `runtimetest.NewFake() *runtimetest.Fake` with exported fields `CreateErr error`, `Cap runtime.Capacity` and method `Spec(ref runtime.Ref) (runtime.EnvironmentSpec, bool)`

- [ ] **Step 1: Write the failing tests**

`internal/runtime/runtime_test.go`:

```go
package runtime

import (
	"errors"
	"testing"
)

func validSpec() EnvironmentSpec {
	return EnvironmentSpec{
		ID: "01k6abcdefghjkmnpqrstvwxyz", Hostname: "ghrm-vwxyz", Cores: 2, MemoryMB: 4096,
		Env: map[string]string{"GHRM_JITCONFIG": "eyJhIjoiYiJ9==", "GHRM_INGEST_URL": "https://10.0.0.2:8443"},
	}
}

func TestSpecValidate(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	cases := map[string]func(*EnvironmentSpec){
		"empty id":           func(s *EnvironmentSpec) { s.ID = "" },
		"uppercase id":       func(s *EnvironmentSpec) { s.ID = "01K6ABC" },
		"bad hostname":       func(s *EnvironmentSpec) { s.Hostname = "Bad_Host" },
		"hostname too long":  func(s *EnvironmentSpec) { s.Hostname = string(make([]byte, 64)) },
		"zero cores":         func(s *EnvironmentSpec) { s.Cores = 0 },
		"tiny memory":        func(s *EnvironmentSpec) { s.MemoryMB = 128 },
		"lowercase env key":  func(s *EnvironmentSpec) { s.Env["lower"] = "x" },
		"empty env key":      func(s *EnvironmentSpec) { s.Env[""] = "x" },
		"newline in value":   func(s *EnvironmentSpec) { s.Env["GHRM_X"] = "a\nb" },
		"nul in value":       func(s *EnvironmentSpec) { s.Env["GHRM_X"] = "a\x00b" },
		"carriage in value":  func(s *EnvironmentSpec) { s.Env["GHRM_X"] = "a\rb" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSpec()
			mutate(&s)
			if err := s.Validate(); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("Validate() = %v, want ErrInvalidSpec", err)
			}
		})
	}
}
```

`internal/runtime/runtimetest/fake_test.go`:

```go
package runtimetest

import (
	"context"
	"errors"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

var _ runtime.Runtime = (*Fake)(nil)

func spec(id string) runtime.EnvironmentSpec {
	return runtime.EnvironmentSpec{ID: id, Hostname: "ghrm-" + id, Cores: 1, MemoryMB: 512}
}

func TestFakeLifecycle(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	ref, err := f.Create(ctx, spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.Create(ctx, spec("aaa"))
	if err != nil || again != ref {
		t.Fatalf("Create must be idempotent per ID: %v %v", again, err)
	}
	if err := f.Start(ctx, ref); err != nil {
		t.Fatal(err)
	}
	st, err := f.Status(ctx, ref)
	if err != nil || !st.Running || st.EnvironmentID != "aaa" || st.IP == "" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	list, _ := f.List(ctx)
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if err := f.Destroy(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err := f.Destroy(ctx, ref); err != nil {
		t.Fatalf("Destroy of a missing environment must be nil, got %v", err)
	}
	if _, err := f.Status(ctx, ref); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Status after destroy = %v, want ErrNotFound", err)
	}
}

func TestFakeCreateErrAndValidation(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	f.CreateErr = errors.New("boom")
	if _, err := f.Create(ctx, spec("bbb")); err == nil {
		t.Fatal("want CreateErr")
	}
	f.CreateErr = nil
	bad := spec("ccc")
	bad.Cores = 0
	if _, err := f.Create(ctx, bad); !errors.Is(err, runtime.ErrInvalidSpec) {
		t.Fatalf("Create(invalid) = %v, want ErrInvalidSpec", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/runtime/...`
Expected: FAIL to build with `undefined: EnvironmentSpec` / `undefined: NewFake`.

- [ ] **Step 3: Write the interface**

`internal/runtime/runtime.go`:

```go
// Package runtime abstracts the infrastructure that runs job environments (spec §4.2).
package runtime

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrNotFound is returned by Status (and Stop) when the environment does not exist.
	ErrNotFound = errors.New("runtime: environment not found")
	// ErrInvalidSpec is wrapped by EnvironmentSpec.Validate errors.
	ErrInvalidSpec = errors.New("runtime: invalid environment spec")
)

// EnvironmentSpec describes one environment to create.
type EnvironmentSpec struct {
	ID       string            // ghrm environment ID: lowercase ULID
	Hostname string            // DNS label
	Cores    int
	MemoryMB int
	Env      map[string]string // variables visible to the guest's init process
}

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9]{1,40}$`)
	hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envKeyPattern   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// Validate rejects specs that a runtime could not apply safely.
func (s EnvironmentSpec) Validate() error {
	var problems []string
	if !idPattern.MatchString(s.ID) {
		problems = append(problems, fmt.Sprintf("id %q must be 1-40 lowercase letters or digits", s.ID))
	}
	if !hostnamePattern.MatchString(s.Hostname) {
		problems = append(problems, fmt.Sprintf("hostname %q is not a valid DNS label", s.Hostname))
	}
	if s.Cores < 1 {
		problems = append(problems, "cores must be at least 1")
	}
	if s.MemoryMB < 256 {
		problems = append(problems, "memory must be at least 256 MB")
	}
	for k, v := range s.Env {
		if !envKeyPattern.MatchString(k) {
			problems = append(problems, fmt.Sprintf("env key %q must match %s", k, envKeyPattern))
		}
		if strings.ContainsAny(v, "\x00\n\r") {
			problems = append(problems, fmt.Sprintf("env %s must not contain NUL or line breaks", k))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidSpec, strings.Join(problems, "; "))
	}
	return nil
}

// Ref identifies an environment inside a runtime, for example a Proxmox VMID.
type Ref struct{ ID string }

func (r Ref) String() string { return r.ID }

// Status is the runtime's view of one environment.
type Status struct {
	Ref           Ref
	EnvironmentID string
	Running       bool
	IP            string
}

// Capacity is the runtime's view of host resources.
type Capacity struct {
	HostMemoryTotalMB     int
	HostMemoryAvailableMB int
	ThinPoolPercent       float64
	Environments          int
}

// Runtime creates and destroys job environments.
type Runtime interface {
	// Create provisions a stopped environment that is ready to start. It is idempotent per spec.ID.
	Create(ctx context.Context, spec EnvironmentSpec) (Ref, error)
	Start(ctx context.Context, ref Ref) error
	Stop(ctx context.Context, ref Ref) error
	// Destroy removes the environment. Destroying a missing environment is not an error.
	Destroy(ctx context.Context, ref Ref) error
	// List returns every environment owned by ghrm in this runtime.
	List(ctx context.Context) ([]Status, error)
	// Status returns ErrNotFound when the environment does not exist.
	Status(ctx context.Context, ref Ref) (Status, error)
	Capacity(ctx context.Context) (Capacity, error)
}
```

- [ ] **Step 4: Write the fake**

`internal/runtime/runtimetest/fake.go`:

```go
// Package runtimetest provides an in-memory runtime.Runtime for tests.
package runtimetest

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

// Fake is an in-memory runtime. It is safe for concurrent use.
type Fake struct {
	mu   sync.Mutex
	next int
	envs map[string]*fakeEnv // keyed by ref ID

	// CreateErr, when non-nil, is returned by Create.
	CreateErr error
	// Cap is returned by Capacity, with Environments filled in.
	Cap runtime.Capacity
}

type fakeEnv struct {
	spec    runtime.EnvironmentSpec
	running bool
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{envs: map[string]*fakeEnv{}} }

func (f *Fake) Create(_ context.Context, spec runtime.EnvironmentSpec) (runtime.Ref, error) {
	if err := spec.Validate(); err != nil {
		return runtime.Ref{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.CreateErr != nil {
		return runtime.Ref{}, f.CreateErr
	}
	for id, e := range f.envs {
		if e.spec.ID == spec.ID {
			return runtime.Ref{ID: id}, nil
		}
	}
	f.next++
	ref := runtime.Ref{ID: fmt.Sprintf("fake-%d", f.next)}
	f.envs[ref.ID] = &fakeEnv{spec: spec}
	return ref, nil
}

func (f *Fake) Start(_ context.Context, ref runtime.Ref) error { return f.setRunning(ref, true) }
func (f *Fake) Stop(_ context.Context, ref runtime.Ref) error  { return f.setRunning(ref, false) }

func (f *Fake) setRunning(ref runtime.Ref, running bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.envs[ref.ID]
	if !ok {
		return runtime.ErrNotFound
	}
	e.running = running
	return nil
}

func (f *Fake) Destroy(_ context.Context, ref runtime.Ref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.envs, ref.ID)
	return nil
}

func (f *Fake) List(_ context.Context) ([]runtime.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runtime.Status, 0, len(f.envs))
	for id, e := range f.envs {
		out = append(out, f.status(id, e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.ID < out[j].Ref.ID })
	return out, nil
}

func (f *Fake) Status(_ context.Context, ref runtime.Ref) (runtime.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.envs[ref.ID]
	if !ok {
		return runtime.Status{}, runtime.ErrNotFound
	}
	return f.status(ref.ID, e), nil
}

func (f *Fake) status(id string, e *fakeEnv) runtime.Status {
	st := runtime.Status{Ref: runtime.Ref{ID: id}, EnvironmentID: e.spec.ID, Running: e.running}
	if e.running {
		st.IP = "192.0.2.10"
	}
	return st
}

func (f *Fake) Capacity(_ context.Context) (runtime.Capacity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.Cap
	c.Environments = len(f.envs)
	return c, nil
}

// Spec returns the spec an environment was created with.
func (f *Fake) Spec(ref runtime.Ref) (runtime.EnvironmentSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.envs[ref.ID]
	if !ok {
		return runtime.EnvironmentSpec{}, false
	}
	return e.spec, true
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/runtime/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime
git commit -m "feat(runtime): define the runtime interface and an in-memory fake"
```

---

### Task 6: Proxmox API client and fake server

**Files:**
- Create: `internal/proxmox/client.go`
- Create: `internal/proxmox/task.go`
- Create: `internal/proxmox/lxc.go`
- Create: `internal/proxmox/node.go`
- Create: `internal/proxmox/proxmoxtest/server.go`
- Test: `internal/proxmox/client_test.go` (package `proxmox_test`)

**Interfaces:**
- Produces (package `proxmox`):
  - `proxmox.New(cfg proxmox.Config) (*proxmox.Client, error)`; `proxmox.Config{URL, TokenID, TokenSecret string; InsecureSkipVerify bool; HTTPClient *http.Client}`
  - `(*Client).PollInterval time.Duration` (exported field; default 500 ms)
  - `proxmox.ErrNotFound`, `*proxmox.APIError`
  - `(*Client) WaitTask(ctx, node, upid string) error`
  - `(*Client) ListLXC(ctx, node string) ([]proxmox.LXC, error)`; `proxmox.LXC{VMID int; Name, Status, Tags string; Template int}` with `HasTag(tag string) bool` and `TagList() []string`
  - `(*Client) CloneLXC(ctx, node string, source, target int, opts proxmox.CloneOptions) error`; `proxmox.CloneOptions{Hostname, Description, Pool string; Full bool}`
  - `(*Client) SetLXCConfig(ctx, node string, vmid int, values url.Values) error`
  - `(*Client) LXCConfig(ctx, node string, vmid int) (map[string]any, error)`
  - `(*Client) StartLXC / StopLXC / DeleteLXC(ctx, node string, vmid int) error`
  - `(*Client) LXCCurrentStatus(ctx, node string, vmid int) (proxmox.LXCStatus, error)`; `proxmox.LXCStatus{Status string; Mem, MaxMem int64}`
  - `(*Client) LXCInterfaces(ctx, node string, vmid int) ([]proxmox.Interface, error)`; `proxmox.Interface{Name, Inet string}`
  - `(*Client) NodeStatus(ctx, node string) (proxmox.NodeStatus, error)`; `proxmox.NodeStatus{Memory proxmox.NodeMemory}`; `proxmox.NodeMemory{Total, Free, Available int64}`
  - `(*Client) ThinPools(ctx, node string) ([]proxmox.ThinPool, error)`; `proxmox.ThinPool{LV string; Size, Used, MetadataSize, MetadataUsed int64}`
  - `(*Client) VMIDAvailable(ctx context.Context, vmid int) (bool, error)`
- Produces (package `proxmoxtest`):
  - `proxmoxtest.NewServer(t testing.TB, node, tokenID, tokenSecret string) *proxmoxtest.Server` (embeds `*httptest.Server`)
  - `(*Server) AddGuest(g proxmoxtest.Guest)`; `proxmoxtest.Guest{VMID int; Type, Name, Status, Tags string; Template bool; Config map[string]string}`
  - `(*Server) Guest(vmid int) (proxmoxtest.Guest, bool)`, `(*Server) Requests() []string`
  - Exported fields: `FailConfigPut bool`, `FailStart bool`, `MemoryTotal, MemoryAvailable int64`, `ThinPools []proxmoxtest.ThinPool`
  - `proxmoxtest.ThinPool{LV string; Size, Used, MetadataSize, MetadataUsed int64}`

Real Proxmox puts error reasons in the HTTP status line (for example `500 Configuration file 'nodes/pve/lxc/950.conf' does not exist`). `APIError` keeps both the status line and the body, and `errors.Is(err, ErrNotFound)` matches either a 404 or a "does not exist" reason. The fake server puts the reason in a `message` body field, because `httptest` cannot set a custom reason phrase.

- [ ] **Step 1: Write the fake server**

`internal/proxmox/proxmoxtest/server.go`:

```go
// Package proxmoxtest provides an in-process fake of the Proxmox VE API subset used by ghrm.
package proxmoxtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
)

// Guest is a fake LXC or QEMU guest.
type Guest struct {
	VMID     int
	Type     string // "lxc" or "qemu"
	Name     string
	Status   string // "running" or "stopped"
	Tags     string
	Template bool
	Config   map[string]string
}

// ThinPool mirrors /nodes/{node}/disks/lvmthin entries.
type ThinPool struct {
	LV           string `json:"lv"`
	Size         int64  `json:"lv_size"`
	Used         int64  `json:"used"`
	MetadataSize int64  `json:"metadata_size"`
	MetadataUsed int64  `json:"metadata_used"`
}

// Server is a fake Proxmox API. Tasks complete immediately.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	node     string
	auth     string
	guests   map[int]*Guest
	tasks    map[string]string // upid -> exit status
	requests []string

	FailConfigPut   bool
	FailStart       bool
	MemoryTotal     int64
	MemoryAvailable int64
	ThinPools       []ThinPool
}

// NewServer starts a fake server that accepts the given API token. It is closed when the test ends.
func NewServer(t testing.TB, node, tokenID, tokenSecret string) *Server {
	s := &Server{
		node:            node,
		auth:            "PVEAPIToken=" + tokenID + "=" + tokenSecret,
		guests:          map[int]*Guest{},
		tasks:           map[string]string{},
		MemoryTotal:     32 << 30,
		MemoryAvailable: 24 << 30,
	}
	mux := http.NewServeMux()
	p := "/api2/json"
	mux.HandleFunc("GET "+p+"/cluster/nextid", s.nextID)
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc", s.listLXC)
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/clone", s.clone)
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc/{vmid}/config", s.getConfig)
	mux.HandleFunc("PUT "+p+"/nodes/{node}/lxc/{vmid}/config", s.putConfig)
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/status/start", s.power("running"))
	mux.HandleFunc("POST "+p+"/nodes/{node}/lxc/{vmid}/status/stop", s.power("stopped"))
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc/{vmid}/status/current", s.currentStatus)
	mux.HandleFunc("GET "+p+"/nodes/{node}/lxc/{vmid}/interfaces", s.interfaces)
	mux.HandleFunc("DELETE "+p+"/nodes/{node}/lxc/{vmid}", s.deleteLXC)
	mux.HandleFunc("GET "+p+"/nodes/{node}/tasks/{upid}/status", s.taskStatus)
	mux.HandleFunc("GET "+p+"/nodes/{node}/tasks/{upid}/log", s.taskLog)
	mux.HandleFunc("GET "+p+"/nodes/{node}/status", s.nodeStatus)
	mux.HandleFunc("GET "+p+"/nodes/{node}/disks/lvmthin", s.lvmthin)
	s.Server = httptest.NewTLSServer(s.authenticate(mux))
	t.Cleanup(s.Close)
	return s
}

// AddGuest registers a guest.
func (s *Server) AddGuest(g Guest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Config == nil {
		g.Config = map[string]string{}
	}
	if g.Status == "" {
		g.Status = "stopped"
	}
	s.guests[g.VMID] = &g
}

// Guest returns a copy of a guest.
func (s *Server) Guest(vmid int) (Guest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guests[vmid]
	if !ok {
		return Guest{}, false
	}
	c := *g
	c.Config = maps.Clone(g.Config)
	return c, true
}

// Requests returns "METHOD path" for every authenticated request, in order.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != s.auth {
			fail(w, http.StatusUnauthorized, "authentication failure")
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func data(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "message": msg})
}

func (s *Server) guestOr404(w http.ResponseWriter, r *http.Request) (*Guest, bool) {
	vmid, _ := strconv.Atoi(r.PathValue("vmid"))
	g, ok := s.guests[vmid]
	if !ok {
		fail(w, http.StatusInternalServerError, fmt.Sprintf("Configuration file 'nodes/%s/lxc/%d.conf' does not exist", s.node, vmid))
	}
	return g, ok
}

func (s *Server) task(kind string, vmid int, exit string) string {
	upid := fmt.Sprintf("UPID:%s:%08d:%s:%d:root@pam:", s.node, len(s.tasks)+1, kind, vmid)
	s.tasks[upid] = exit
	return upid
}

func (s *Server) nextID(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vmid, _ := strconv.Atoi(r.URL.Query().Get("vmid"))
	if _, taken := s.guests[vmid]; taken {
		fail(w, http.StatusBadRequest, fmt.Sprintf("VM %d already exists", vmid))
		return
	}
	data(w, strconv.Itoa(vmid))
}

func (s *Server) listLXC(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	ids := make([]int, 0, len(s.guests))
	for id := range s.guests {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		g := s.guests[id]
		if g.Type != "lxc" {
			continue
		}
		e := map[string]any{"vmid": g.VMID, "name": g.Name, "status": g.Status, "tags": g.Tags}
		if g.Template {
			e["template"] = 1
		}
		out = append(out, e)
	}
	data(w, out)
}

func (s *Server) clone(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	newID, _ := strconv.Atoi(r.PostForm.Get("newid"))
	if _, taken := s.guests[newID]; taken {
		fail(w, http.StatusInternalServerError, fmt.Sprintf("CT %d already exists", newID))
		return
	}
	g := &Guest{VMID: newID, Type: "lxc", Status: "stopped", Tags: src.Tags, Name: r.PostForm.Get("hostname"), Config: maps.Clone(src.Config)}
	for _, k := range []string{"hostname", "description", "pool"} {
		if v := r.PostForm.Get(k); v != "" {
			g.Config[k] = v
		}
	}
	g.Config["linked"] = r.PostForm.Get("full")
	s.guests[newID] = g
	data(w, s.task("vzclone", newID, "OK"))
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		data(w, g.Config)
	}
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	if s.FailConfigPut {
		fail(w, http.StatusInternalServerError, "config update failed")
		return
	}
	_ = r.ParseForm()
	for k, v := range r.PostForm {
		g.Config[k] = v[0]
		if k == "tags" {
			g.Tags = v[0]
		}
	}
	data(w, nil)
}

func (s *Server) power(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		g, ok := s.guestOr404(w, r)
		if !ok {
			return
		}
		if state == "running" && s.FailStart {
			data(w, s.task("vzstart", g.VMID, "command 'lxc-start' failed: exit code 1"))
			return
		}
		g.Status = state
		data(w, s.task("vz"+state, g.VMID, "OK"))
	}
}

func (s *Server) currentStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.guestOr404(w, r); ok {
		mem, _ := strconv.ParseInt(g.Config["memory"], 10, 64)
		data(w, map[string]any{"status": g.Status, "mem": 256 << 20, "maxmem": mem << 20})
	}
}

func (s *Server) interfaces(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	if g.Status != "running" {
		data(w, []any{})
		return
	}
	data(w, []map[string]string{
		{"name": "lo", "inet": "127.0.0.1/8"},
		{"name": "eth0", "inet": fmt.Sprintf("10.50.0.%d/24", 100+g.VMID%100)},
	})
}

func (s *Server) deleteLXC(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.guestOr404(w, r)
	if !ok {
		return
	}
	if g.Status == "running" {
		fail(w, http.StatusInternalServerError, fmt.Sprintf("CT %d is running - destroy failed", g.VMID))
		return
	}
	delete(s.guests, g.VMID)
	data(w, s.task("vzdestroy", g.VMID, "OK"))
}

func (s *Server) taskStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exit, ok := s.tasks[r.PathValue("upid")]
	if !ok {
		fail(w, http.StatusInternalServerError, "no such task")
		return
	}
	data(w, map[string]string{"status": "stopped", "exitstatus": exit})
}

func (s *Server) taskLog(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exit := s.tasks[r.PathValue("upid")]
	data(w, []map[string]any{{"n": 1, "t": "starting task"}, {"n": 2, "t": exit}})
}

func (s *Server) nodeStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data(w, map[string]any{"memory": map[string]int64{
		"total": s.MemoryTotal, "available": s.MemoryAvailable, "free": s.MemoryAvailable / 2,
	}})
}

func (s *Server) lvmthin(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data(w, s.ThinPools)
}
```

- [ ] **Step 2: Write the failing client tests**

`internal/proxmox/client_test.go`:

```go
package proxmox_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox/proxmoxtest"
)

const (
	node    = "pve"
	tokenID = "ghrm@pve!ghrm"
	secret  = "s3cret"
)

func newClient(t *testing.T, srv *proxmoxtest.Server, tokenSecret string) *proxmox.Client {
	t.Helper()
	c, err := proxmox.New(proxmox.Config{URL: srv.URL, TokenID: tokenID, TokenSecret: tokenSecret, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	c.PollInterval = time.Millisecond
	return c
}

func setup(t *testing.T) (*proxmoxtest.Server, *proxmox.Client) {
	srv := proxmoxtest.NewServer(t, node, tokenID, secret)
	srv.AddGuest(proxmoxtest.Guest{VMID: 9000, Type: "lxc", Name: "ghrm-template", Template: true, Tags: "ghrm-template", Config: map[string]string{"memory": "4096"}})
	return srv, newClient(t, srv, secret)
}

func TestWrongTokenIsUnauthorized(t *testing.T) {
	srv, _ := setup(t)
	c := newClient(t, srv, "wrong")
	_, err := c.ListLXC(context.Background(), node)
	var apiErr *proxmox.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 {
		t.Fatalf("err = %v, want 401 APIError", err)
	}
}

func TestLXCLifecycle(t *testing.T) {
	srv, c := setup(t)
	ctx := context.Background()

	err := c.CloneLXC(ctx, node, 9000, 901, proxmox.CloneOptions{Hostname: "ghrm-a", Description: "env a", Pool: "ghrm"})
	if err != nil {
		t.Fatal(err)
	}
	g, ok := srv.Guest(901)
	if !ok || g.Name != "ghrm-a" || g.Config["pool"] != "ghrm" || g.Config["linked"] != "0" {
		t.Fatalf("clone = %+v (exists %v), want linked clone named ghrm-a in pool ghrm", g, ok)
	}

	if err := c.SetLXCConfig(ctx, node, 901, url.Values{"cores": {"2"}, "env": {"A=1\x00B=2"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := c.LXCConfig(ctx, node, 901)
	if err != nil || cfg["cores"] != "2" || cfg["env"] != "A=1\x00B=2" {
		t.Fatalf("config = %v, %v", cfg, err)
	}

	if err := c.StartLXC(ctx, node, 901); err != nil {
		t.Fatal(err)
	}
	st, err := c.LXCCurrentStatus(ctx, node, 901)
	if err != nil || st.Status != "running" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	ifaces, err := c.LXCInterfaces(ctx, node, 901)
	if err != nil || len(ifaces) != 2 || ifaces[1].Name != "eth0" {
		t.Fatalf("interfaces = %+v, %v", ifaces, err)
	}

	list, err := c.ListLXC(ctx, node)
	if err != nil || len(list) != 2 || list[1].VMID != 901 || list[0].Template != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if !list[0].HasTag("ghrm-template") || list[1].HasTag("ghrm-env") {
		t.Fatalf("tags = %q / %q", list[0].Tags, list[1].Tags)
	}

	if err := c.StopLXC(ctx, node, 901); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteLXC(ctx, node, 901); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Guest(901); ok {
		t.Fatal("guest still exists after delete")
	}
}

func TestDeleteMissingIsNotFound(t *testing.T) {
	_, c := setup(t)
	err := c.DeleteLXC(context.Background(), node, 950)
	if !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	_, err = c.LXCCurrentStatus(context.Background(), node, 950)
	if !errors.Is(err, proxmox.ErrNotFound) {
		t.Fatalf("status err = %v, want ErrNotFound", err)
	}
}

func TestFailedTaskReturnsLogTail(t *testing.T) {
	srv, c := setup(t)
	srv.AddGuest(proxmoxtest.Guest{VMID: 902, Type: "lxc"})
	srv.FailStart = true
	err := c.StartLXC(context.Background(), node, 902)
	if err == nil || !strings.Contains(err.Error(), "lxc-start") {
		t.Fatalf("err = %v, want task failure with log tail", err)
	}
}

func TestTaskWaitHonoursContext(t *testing.T) {
	_, c := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.WaitTask(ctx, node, "UPID:pve:1:x:1:root@pam:"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestVMIDAvailable(t *testing.T) {
	srv, c := setup(t)
	srv.AddGuest(proxmoxtest.Guest{VMID: 905, Type: "qemu"})
	ok, err := c.VMIDAvailable(context.Background(), 905)
	if err != nil || ok {
		t.Fatalf("VMIDAvailable(905) = %v, %v, want false", ok, err)
	}
	ok, err = c.VMIDAvailable(context.Background(), 906)
	if err != nil || !ok {
		t.Fatalf("VMIDAvailable(906) = %v, %v, want true", ok, err)
	}
}

func TestNodeStatusAndThinPools(t *testing.T) {
	srv, c := setup(t)
	srv.ThinPools = []proxmoxtest.ThinPool{{LV: "data", Size: 1000, Used: 400, MetadataSize: 100, MetadataUsed: 10}}
	ns, err := c.NodeStatus(context.Background(), node)
	if err != nil || ns.Memory.Total != 32<<30 || ns.Memory.Available != 24<<30 {
		t.Fatalf("node status = %+v, %v", ns, err)
	}
	pools, err := c.ThinPools(context.Background(), node)
	if err != nil || len(pools) != 1 || pools[0].LV != "data" || pools[0].Used != 400 || pools[0].MetadataUsed != 10 {
		t.Fatalf("thin pools = %+v, %v", pools, err)
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	if _, err := proxmox.New(proxmox.Config{URL: "::not a url"}); err == nil {
		t.Fatal("want error for invalid URL")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/proxmox/...`
Expected: FAIL to build with `undefined: proxmox.New` (and the other client symbols).

- [ ] **Step 4: Write the client core**

`internal/proxmox/client.go`:

```go
// Package proxmox is a small client for the parts of the Proxmox VE API that ghrm uses.
// It authenticates with API tokens only.
package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound matches API errors for guests or objects that do not exist.
var ErrNotFound = errors.New("proxmox: not found")

// Config configures a Client.
type Config struct {
	URL                string // e.g. https://pve.example.test:8006
	TokenID            string // user@realm!tokenname
	TokenSecret        string
	InsecureSkipVerify bool         // accept self-signed certificates (opt-in)
	HTTPClient         *http.Client // optional; when set, InsecureSkipVerify is ignored
}

// Client talks to one Proxmox VE API endpoint.
type Client struct {
	base string
	auth string
	http *http.Client

	// PollInterval is the delay between task status polls.
	PollInterval time.Duration
}

// APIError is a non-2xx API response.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Status     string // full status line; Proxmox puts the reason here
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("proxmox %s %s: %s %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

// Is lets errors.Is(err, ErrNotFound) match missing objects.
func (e *APIError) Is(target error) bool {
	return target == ErrNotFound &&
		(e.StatusCode == http.StatusNotFound || strings.Contains(e.Status+" "+e.Body, "does not exist"))
}

// New returns a Client for cfg.
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("proxmox: invalid URL %q", cfg.URL)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in for self-signed homelab certificates
		hc = &http.Client{Transport: tr, Timeout: 60 * time.Second}
	}
	return &Client{
		base:         strings.TrimRight(u.String(), "/") + "/api2/json",
		auth:         "PVEAPIToken=" + cfg.TokenID + "=" + cfg.TokenSecret,
		http:         hc,
		PollInterval: 500 * time.Millisecond,
	}, nil
}

// do performs a request. params go in the query string for GET/DELETE and in a
// form body otherwise. When out is non-nil, the response's "data" field is decoded into it.
func (c *Client) do(ctx context.Context, method, path string, params url.Values, out any) error {
	target := c.base + path
	var body io.Reader
	if len(params) > 0 {
		if method == http.MethodGet || method == http.MethodDelete {
			target += "?" + params.Encode()
		} else {
			body = strings.NewReader(params.Encode())
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("proxmox %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("proxmox %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Status: resp.Status, Body: string(raw)}
	}
	if out == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("proxmox %s %s: decode: %w", method, path, err)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("proxmox %s %s: decode data: %w", method, path, err)
	}
	return nil
}

func lxcPath(node string, vmid int, suffix string) string {
	return fmt.Sprintf("/nodes/%s/lxc/%d%s", url.PathEscape(node), vmid, suffix)
}
```

- [ ] **Step 5: Write task handling**

`internal/proxmox/task.go`:

```go
package proxmox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WaitTask polls a task until it stops. Exit status "OK" or "WARNINGS: n" is success.
func (c *Client) WaitTask(ctx context.Context, node, upid string) error {
	path := fmt.Sprintf("/nodes/%s/tasks/%s", url.PathEscape(node), url.PathEscape(upid))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var st struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := c.do(ctx, http.MethodGet, path+"/status", nil, &st); err != nil {
			return err
		}
		if st.Status == "stopped" {
			if st.ExitStatus == "OK" || strings.HasPrefix(st.ExitStatus, "WARNINGS") {
				return nil
			}
			return fmt.Errorf("proxmox task %s failed: %s%s", upid, st.ExitStatus, c.taskLogTail(ctx, path))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.PollInterval):
		}
	}
}

func (c *Client) taskLogTail(ctx context.Context, path string) string {
	var lines []struct {
		T string `json:"t"`
	}
	if err := c.do(ctx, http.MethodGet, path+"/log", url.Values{"limit": {"200"}}, &lines); err != nil || len(lines) == 0 {
		return ""
	}
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = l.T
	}
	return "\n" + strings.Join(parts, "\n")
}

// postTask issues a request that returns a UPID and waits for the task.
func (c *Client) postTask(ctx context.Context, node, method, path string, params url.Values) error {
	var upid string
	if err := c.do(ctx, method, path, params, &upid); err != nil {
		return err
	}
	return c.WaitTask(ctx, node, upid)
}
```

- [ ] **Step 6: Write LXC and node operations**

`internal/proxmox/lxc.go`:

```go
package proxmox

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// LXC is an entry of GET /nodes/{node}/lxc.
type LXC struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Tags     string `json:"tags"`
	Template int    `json:"template"`
}

// TagList splits the Proxmox tag string.
func (l LXC) TagList() []string {
	return strings.FieldsFunc(l.Tags, func(r rune) bool { return r == ';' || r == ',' || r == ' ' })
}

// HasTag reports whether the guest carries tag.
func (l LXC) HasTag(tag string) bool {
	for _, t := range l.TagList() {
		if t == tag {
			return true
		}
	}
	return false
}

// ListLXC lists the LXC guests on node.
func (c *Client) ListLXC(ctx context.Context, node string) ([]LXC, error) {
	var out []LXC
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/lxc", nil, &out)
	return out, err
}

// CloneOptions configures CloneLXC.
type CloneOptions struct {
	Hostname    string
	Description string
	Pool        string
	Full        bool // false = linked clone (requires a template)
}

// CloneLXC clones source into target and waits for the task.
func (c *Client) CloneLXC(ctx context.Context, node string, source, target int, opts CloneOptions) error {
	params := url.Values{"newid": {strconv.Itoa(target)}, "full": {"0"}}
	if opts.Full {
		params.Set("full", "1")
	}
	if opts.Hostname != "" {
		params.Set("hostname", opts.Hostname)
	}
	if opts.Description != "" {
		params.Set("description", opts.Description)
	}
	if opts.Pool != "" {
		params.Set("pool", opts.Pool)
	}
	return c.postTask(ctx, node, http.MethodPost, lxcPath(node, source, "/clone"), params)
}

// SetLXCConfig updates configuration options synchronously.
func (c *Client) SetLXCConfig(ctx context.Context, node string, vmid int, values url.Values) error {
	return c.do(ctx, http.MethodPut, lxcPath(node, vmid, "/config"), values, nil)
}

// LXCConfig returns the guest configuration.
func (c *Client) LXCConfig(ctx context.Context, node string, vmid int) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodGet, lxcPath(node, vmid, "/config"), nil, &out)
	return out, err
}

// StartLXC starts a guest and waits for the task.
func (c *Client) StartLXC(ctx context.Context, node string, vmid int) error {
	return c.postTask(ctx, node, http.MethodPost, lxcPath(node, vmid, "/status/start"), nil)
}

// StopLXC stops a guest immediately and waits for the task.
func (c *Client) StopLXC(ctx context.Context, node string, vmid int) error {
	return c.postTask(ctx, node, http.MethodPost, lxcPath(node, vmid, "/status/stop"), nil)
}

// DeleteLXC destroys a stopped guest, purging it from jobs and ACLs.
func (c *Client) DeleteLXC(ctx context.Context, node string, vmid int) error {
	params := url.Values{"purge": {"1"}, "destroy-unreferenced-disks": {"1"}}
	return c.postTask(ctx, node, http.MethodDelete, lxcPath(node, vmid, ""), params)
}

// LXCStatus is GET /nodes/{node}/lxc/{vmid}/status/current.
type LXCStatus struct {
	Status string `json:"status"`
	Mem    int64  `json:"mem"`
	MaxMem int64  `json:"maxmem"`
}

// LXCCurrentStatus returns the live status of a guest.
func (c *Client) LXCCurrentStatus(ctx context.Context, node string, vmid int) (LXCStatus, error) {
	var out LXCStatus
	err := c.do(ctx, http.MethodGet, lxcPath(node, vmid, "/status/current"), nil, &out)
	return out, err
}

// Interface is an entry of GET /nodes/{node}/lxc/{vmid}/interfaces.
type Interface struct {
	Name string `json:"name"`
	Inet string `json:"inet"`
}

// LXCInterfaces returns the network interfaces of a running guest.
func (c *Client) LXCInterfaces(ctx context.Context, node string, vmid int) ([]Interface, error) {
	var out []Interface
	err := c.do(ctx, http.MethodGet, lxcPath(node, vmid, "/interfaces"), nil, &out)
	return out, err
}
```

`internal/proxmox/node.go`:

```go
package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// NodeMemory is the memory section of GET /nodes/{node}/status (bytes).
type NodeMemory struct {
	Total     int64 `json:"total"`
	Free      int64 `json:"free"`
	Available int64 `json:"available"`
}

// NodeStatus is GET /nodes/{node}/status.
type NodeStatus struct {
	Memory NodeMemory `json:"memory"`
}

// NodeStatus returns node resource usage.
func (c *Client) NodeStatus(ctx context.Context, node string) (NodeStatus, error) {
	var out NodeStatus
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/status", nil, &out)
	return out, err
}

// ThinPool is an entry of GET /nodes/{node}/disks/lvmthin (bytes).
type ThinPool struct {
	LV           string `json:"lv"`
	Size         int64  `json:"lv_size"`
	Used         int64  `json:"used"`
	MetadataSize int64  `json:"metadata_size"`
	MetadataUsed int64  `json:"metadata_used"`
}

// ThinPools lists LVM thin pools on node.
func (c *Client) ThinPools(ctx context.Context, node string) ([]ThinPool, error) {
	var out []ThinPool
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/disks/lvmthin", nil, &out)
	return out, err
}

// VMIDAvailable reports whether no guest in the cluster uses vmid. It sees guests
// the token has no permission on, unlike listing endpoints.
func (c *Client) VMIDAvailable(ctx context.Context, vmid int) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/cluster/nextid", url.Values{"vmid": {strconv.Itoa(vmid)}}, nil)
	var apiErr *APIError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest && strings.Contains(apiErr.Status+apiErr.Body, "already exists"):
		return false, nil
	default:
		return false, err
	}
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -race ./internal/proxmox/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/proxmox
git commit -m "feat(proxmox): add a token-authenticated API client and a fake server"
```

---

### Task 7: `proxmox-lxc` runtime

**Files:**
- Create: `internal/runtime/proxmoxlxc/runtime.go`
- Test: `internal/runtime/proxmoxlxc/runtime_test.go`

**Interfaces:**
- Consumes: `runtime.Runtime`, `runtime.EnvironmentSpec`, `runtime.ErrNotFound` (Task 5); `proxmox.Client` and its methods, `proxmox.ErrNotFound` (Task 6); `proxmoxtest.Server` (Task 6).
- Produces:
  - `proxmoxlxc.New(client *proxmox.Client, cfg proxmoxlxc.Config) *proxmoxlxc.Runtime`
  - `proxmoxlxc.Config{Node string; TemplateVMID int; Pool string; VMIDStart, VMIDEnd int; ThinPool string; FirewallSettle time.Duration}`
  - `proxmoxlxc.ErrNoFreeVMID`, `proxmoxlxc.TagEnvironment = "ghrm-env"`
  - `(*Runtime) SetSleep(func(context.Context, time.Duration) error)` for tests

Behaviour:
- `Create` validates the spec, then takes a mutex so that two creates cannot pick the same VMID. It returns the existing ref if a guest already carries `ghrmid-<id>`. It picks the lowest VMID in range that is not taken (`VMIDAvailable`), linked-clones the template, sets `cores`, `memory`, `swap=0`, `tags` and `env` (sorted `KEY=VALUE` pairs joined by NUL), then waits `FirewallSettle`. On any failure after the clone, it destroys the clone (using a context that survives cancellation, bounded to 2 minutes).
- `Destroy` stops a running guest, then deletes it. A missing guest is `nil`.
- `Stop` on a missing guest returns `runtime.ErrNotFound`.
- `Status` maps a missing guest to `runtime.ErrNotFound` and fills in the eth0 IPv4 address (without the prefix length) when the guest is running.
- `List` returns guests tagged `ghrm-env`.
- `Capacity` reads node memory and uses the larger of the data and metadata usage percentages of `ThinPool`.

- [ ] **Step 1: Write the failing tests**

`internal/runtime/proxmoxlxc/runtime_test.go`:

```go
package proxmoxlxc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox/proxmoxtest"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

var _ runtime.Runtime = (*Runtime)(nil)

type harness struct {
	srv    *proxmoxtest.Server
	rt     *Runtime
	sleeps []time.Duration
}

func newHarness(t *testing.T, start, end int) *harness {
	t.Helper()
	srv := proxmoxtest.NewServer(t, "pve", "ghrm@pve!ghrm", "s3cret")
	srv.AddGuest(proxmoxtest.Guest{VMID: 9000, Type: "lxc", Name: "ghrm-template", Template: true, Tags: "ghrm-template", Config: map[string]string{"memory": "4096"}})
	client, err := proxmox.New(proxmox.Config{URL: srv.URL, TokenID: "ghrm@pve!ghrm", TokenSecret: "s3cret", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	client.PollInterval = time.Millisecond
	h := &harness{srv: srv}
	h.rt = New(client, Config{Node: "pve", TemplateVMID: 9000, Pool: "ghrm", VMIDStart: start, VMIDEnd: end, ThinPool: "data", FirewallSettle: 12 * time.Second})
	h.rt.SetSleep(func(ctx context.Context, d time.Duration) error {
		h.sleeps = append(h.sleeps, d)
		return ctx.Err()
	})
	return h
}

func spec(id string) runtime.EnvironmentSpec {
	return runtime.EnvironmentSpec{
		ID: id, Hostname: "ghrm-" + id, Cores: 2, MemoryMB: 4096,
		Env: map[string]string{"GHRM_JITCONFIG": "abc==", "GHRM_ENV_ID": id},
	}
}

func TestCreateConfiguresCloneAndWaitsForFirewall(t *testing.T) {
	h := newHarness(t, 900, 909)
	ref, err := h.rt.Create(context.Background(), spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "900" {
		t.Fatalf("ref = %v, want 900", ref)
	}
	g, _ := h.srv.Guest(900)
	want := map[string]string{
		"cores": "2", "memory": "4096", "swap": "0", "pool": "ghrm", "linked": "0",
		"hostname": "ghrm-aaa", "env": "GHRM_ENV_ID=aaa\x00GHRM_JITCONFIG=abc==",
	}
	for k, v := range want {
		if g.Config[k] != v {
			t.Errorf("config[%s] = %q, want %q", k, g.Config[k], v)
		}
	}
	if g.Tags != "ghrm-env;ghrmid-aaa" {
		t.Errorf("tags = %q", g.Tags)
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 12*time.Second {
		t.Errorf("sleeps = %v, want one 12s firewall settle", h.sleeps)
	}
}

func TestCreateIsIdempotentPerID(t *testing.T) {
	h := newHarness(t, 900, 909)
	ctx := context.Background()
	first, err := h.rt.Create(ctx, spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.rt.Create(ctx, spec("aaa"))
	if err != nil || second != first {
		t.Fatalf("second Create = %v, %v; want %v", second, err, first)
	}
	clones := 0
	for _, r := range h.srv.Requests() {
		if strings.HasSuffix(r, "/clone") {
			clones++
		}
	}
	if clones != 1 {
		t.Fatalf("clone requests = %d, want 1", clones)
	}
}

func TestCreateSkipsVMIDsTakenOutsidePool(t *testing.T) {
	h := newHarness(t, 900, 909)
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 900, Type: "qemu", Name: "someone-elses-vm"})
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 901, Type: "lxc", Name: "unrelated"})
	ref, err := h.rt.Create(context.Background(), spec("aaa"))
	if err != nil || ref.ID != "902" {
		t.Fatalf("Create = %v, %v; want 902", ref, err)
	}
}

func TestCreateFailsWhenRangeExhausted(t *testing.T) {
	h := newHarness(t, 900, 901)
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 900, Type: "lxc"})
	h.srv.AddGuest(proxmoxtest.Guest{VMID: 901, Type: "qemu"})
	_, err := h.rt.Create(context.Background(), spec("aaa"))
	if !errors.Is(err, ErrNoFreeVMID) {
		t.Fatalf("err = %v, want ErrNoFreeVMID", err)
	}
}

func TestCreateCleansUpWhenConfigFails(t *testing.T) {
	h := newHarness(t, 900, 909)
	h.srv.FailConfigPut = true
	if _, err := h.rt.Create(context.Background(), spec("aaa")); err == nil {
		t.Fatal("want error")
	}
	if _, exists := h.srv.Guest(900); exists {
		t.Fatal("clone leaked after config failure")
	}
}

func TestCreateCleansUpWhenCancelledDuringSettle(t *testing.T) {
	h := newHarness(t, 900, 909)
	ctx, cancel := context.WithCancel(context.Background())
	h.rt.SetSleep(func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	})
	if _, err := h.rt.Create(ctx, spec("aaa")); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, exists := h.srv.Guest(900); exists {
		t.Fatal("clone leaked after cancellation")
	}
}

func TestCreateRejectsInvalidSpecWithoutAPICalls(t *testing.T) {
	h := newHarness(t, 900, 909)
	bad := spec("aaa")
	bad.Env["GHRM_X"] = "line\nbreak"
	if _, err := h.rt.Create(context.Background(), bad); !errors.Is(err, runtime.ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
	if n := len(h.srv.Requests()); n != 0 {
		t.Fatalf("made %d API calls for an invalid spec", n)
	}
}

func TestStartStatusListDestroy(t *testing.T) {
	h := newHarness(t, 900, 909)
	ctx := context.Background()
	ref, err := h.rt.Create(ctx, spec("aaa"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.rt.Start(ctx, ref); err != nil {
		t.Fatal(err)
	}
	st, err := h.rt.Status(ctx, ref)
	if err != nil || !st.Running || st.EnvironmentID != "aaa" || st.IP != "10.50.0.100" {
		t.Fatalf("Status = %+v, %v", st, err)
	}
	list, err := h.rt.List(ctx)
	if err != nil || len(list) != 1 || list[0].EnvironmentID != "aaa" || list[0].Ref != ref {
		t.Fatalf("List = %+v, %v (the template must not be listed)", list, err)
	}
	if err := h.rt.Destroy(ctx, ref); err != nil {
		t.Fatalf("Destroy of a running environment = %v", err)
	}
	if _, err := h.rt.Status(ctx, ref); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Status after Destroy = %v, want ErrNotFound", err)
	}
}

func TestDestroyMissingIsNoop(t *testing.T) {
	h := newHarness(t, 900, 909)
	if err := h.rt.Destroy(context.Background(), runtime.Ref{ID: "905"}); err != nil {
		t.Fatalf("Destroy(missing) = %v, want nil", err)
	}
	if err := h.rt.Stop(context.Background(), runtime.Ref{ID: "905"}); !errors.Is(err, runtime.ErrNotFound) {
		t.Fatalf("Stop(missing) = %v, want ErrNotFound", err)
	}
}

func TestRejectsMalformedRef(t *testing.T) {
	h := newHarness(t, 900, 909)
	if _, err := h.rt.Status(context.Background(), runtime.Ref{ID: "abc"}); err == nil {
		t.Fatal("want error for non-numeric ref")
	}
}

func TestCapacity(t *testing.T) {
	h := newHarness(t, 900, 909)
	h.srv.MemoryTotal, h.srv.MemoryAvailable = 40<<30, 20<<30
	h.srv.ThinPools = []proxmoxtest.ThinPool{
		{LV: "other", Size: 100, Used: 99},
		{LV: "data", Size: 1000, Used: 400, MetadataSize: 100, MetadataUsed: 60},
	}
	if _, err := h.rt.Create(context.Background(), spec("aaa")); err != nil {
		t.Fatal(err)
	}
	c, err := h.rt.Capacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.HostMemoryTotalMB != 40*1024 || c.HostMemoryAvailableMB != 20*1024 || c.ThinPoolPercent != 60 || c.Environments != 1 {
		t.Fatalf("Capacity = %+v", c)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/runtime/proxmoxlxc/`
Expected: FAIL to build with `undefined: New`.

- [ ] **Step 3: Write the implementation**

`internal/runtime/proxmoxlxc/runtime.go`:

```go
// Package proxmoxlxc runs job environments as linked-clone LXC containers on Proxmox VE.
package proxmoxlxc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
)

const (
	// TagEnvironment marks every guest owned by ghrm.
	TagEnvironment = "ghrm-env"
	idTagPrefix    = "ghrmid-"
	cleanupTimeout = 2 * time.Minute
)

// ErrNoFreeVMID means every VMID in the configured range is taken.
var ErrNoFreeVMID = errors.New("proxmoxlxc: no free VMID in range")

// Config configures the runtime.
type Config struct {
	Node           string
	TemplateVMID   int
	Pool           string // optional resource pool for new guests
	VMIDStart      int
	VMIDEnd        int
	ThinPool       string // LV name of the thin pool, e.g. "data"
	FirewallSettle time.Duration
}

// Runtime implements runtime.Runtime on Proxmox LXC.
type Runtime struct {
	client *proxmox.Client
	cfg    Config
	sleep  func(context.Context, time.Duration) error
	mu     sync.Mutex // serializes Create so VMID allocation cannot race
}

// New returns a Runtime.
func New(client *proxmox.Client, cfg Config) *Runtime {
	return &Runtime{client: client, cfg: cfg, sleep: sleepContext}
}

// SetSleep replaces the firewall-settle sleep (tests only).
func (r *Runtime) SetSleep(fn func(context.Context, time.Duration) error) { r.sleep = fn }

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func refFor(vmid int) runtime.Ref { return runtime.Ref{ID: strconv.Itoa(vmid)} }

func vmidOf(ref runtime.Ref) (int, error) {
	vmid, err := strconv.Atoi(ref.ID)
	if err != nil || vmid <= 0 {
		return 0, fmt.Errorf("proxmoxlxc: invalid ref %q", ref.ID)
	}
	return vmid, nil
}

func idTag(id string) string { return idTagPrefix + id }

func environmentID(l proxmox.LXC) string {
	for _, t := range l.TagList() {
		if strings.HasPrefix(t, idTagPrefix) {
			return strings.TrimPrefix(t, idTagPrefix)
		}
	}
	return ""
}

func encodeEnv(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = k + "=" + env[k]
	}
	return strings.Join(pairs, "\x00")
}

// Create implements runtime.Runtime.
func (r *Runtime) Create(ctx context.Context, spec runtime.EnvironmentSpec) (runtime.Ref, error) {
	if err := spec.Validate(); err != nil {
		return runtime.Ref{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Ref{}, err
	}
	for _, g := range guests {
		if g.HasTag(idTag(spec.ID)) {
			return refFor(g.VMID), nil
		}
	}

	vmid, err := r.allocateVMID(ctx)
	if err != nil {
		return runtime.Ref{}, err
	}
	opts := proxmox.CloneOptions{
		Hostname:    spec.Hostname,
		Description: "Managed by gh-runners-manager. Environment " + spec.ID + ".",
		Pool:        r.cfg.Pool,
	}
	if err := r.client.CloneLXC(ctx, r.cfg.Node, r.cfg.TemplateVMID, vmid, opts); err != nil {
		return runtime.Ref{}, fmt.Errorf("clone template %d to %d: %w", r.cfg.TemplateVMID, vmid, err)
	}

	values := url.Values{
		"cores":  {strconv.Itoa(spec.Cores)},
		"memory": {strconv.Itoa(spec.MemoryMB)},
		"swap":   {"0"},
		"tags":   {TagEnvironment + ";" + idTag(spec.ID)},
	}
	if len(spec.Env) > 0 {
		values.Set("env", encodeEnv(spec.Env))
	}
	if err := r.client.SetLXCConfig(ctx, r.cfg.Node, vmid, values); err != nil {
		r.cleanup(ctx, vmid)
		return runtime.Ref{}, fmt.Errorf("configure %d: %w", vmid, err)
	}
	// The firewall rules of a new guest are applied on pve-firewall's next cycle (spec §10.3).
	if err := r.sleep(ctx, r.cfg.FirewallSettle); err != nil {
		r.cleanup(ctx, vmid)
		return runtime.Ref{}, fmt.Errorf("wait for firewall on %d: %w", vmid, err)
	}
	return refFor(vmid), nil
}

func (r *Runtime) allocateVMID(ctx context.Context) (int, error) {
	for vmid := r.cfg.VMIDStart; vmid <= r.cfg.VMIDEnd; vmid++ {
		free, err := r.client.VMIDAvailable(ctx, vmid)
		if err != nil {
			return 0, fmt.Errorf("check VMID %d: %w", vmid, err)
		}
		if free {
			return vmid, nil
		}
	}
	return 0, fmt.Errorf("%w %d-%d", ErrNoFreeVMID, r.cfg.VMIDStart, r.cfg.VMIDEnd)
}

// cleanup destroys a half-created guest even if ctx was cancelled.
func (r *Runtime) cleanup(ctx context.Context, vmid int) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_ = r.destroy(cctx, vmid)
}

// Start implements runtime.Runtime.
func (r *Runtime) Start(ctx context.Context, ref runtime.Ref) error {
	vmid, err := vmidOf(ref)
	if err != nil {
		return err
	}
	return mapNotFound(r.client.StartLXC(ctx, r.cfg.Node, vmid))
}

// Stop implements runtime.Runtime.
func (r *Runtime) Stop(ctx context.Context, ref runtime.Ref) error {
	vmid, err := vmidOf(ref)
	if err != nil {
		return err
	}
	return mapNotFound(r.client.StopLXC(ctx, r.cfg.Node, vmid))
}

// Destroy implements runtime.Runtime.
func (r *Runtime) Destroy(ctx context.Context, ref runtime.Ref) error {
	vmid, err := vmidOf(ref)
	if err != nil {
		return err
	}
	return r.destroy(ctx, vmid)
}

func (r *Runtime) destroy(ctx context.Context, vmid int) error {
	st, err := r.client.LXCCurrentStatus(ctx, r.cfg.Node, vmid)
	if errors.Is(err, proxmox.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Status == "running" {
		if err := r.client.StopLXC(ctx, r.cfg.Node, vmid); err != nil && !errors.Is(err, proxmox.ErrNotFound) {
			return fmt.Errorf("stop %d: %w", vmid, err)
		}
	}
	if err := r.client.DeleteLXC(ctx, r.cfg.Node, vmid); err != nil && !errors.Is(err, proxmox.ErrNotFound) {
		return fmt.Errorf("delete %d: %w", vmid, err)
	}
	return nil
}

// Status implements runtime.Runtime.
func (r *Runtime) Status(ctx context.Context, ref runtime.Ref) (runtime.Status, error) {
	vmid, err := vmidOf(ref)
	if err != nil {
		return runtime.Status{}, err
	}
	cur, err := r.client.LXCCurrentStatus(ctx, r.cfg.Node, vmid)
	if err != nil {
		return runtime.Status{}, mapNotFound(err)
	}
	st := runtime.Status{Ref: ref, Running: cur.Status == "running"}
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Status{}, err
	}
	for _, g := range guests {
		if g.VMID == vmid {
			st.EnvironmentID = environmentID(g)
		}
	}
	if st.Running {
		if ifaces, err := r.client.LXCInterfaces(ctx, r.cfg.Node, vmid); err == nil {
			for _, i := range ifaces {
				if i.Name == "eth0" && i.Inet != "" {
					st.IP, _, _ = strings.Cut(i.Inet, "/")
				}
			}
		}
	}
	return st, nil
}

// List implements runtime.Runtime.
func (r *Runtime) List(ctx context.Context) ([]runtime.Status, error) {
	guests, err := r.client.ListLXC(ctx, r.cfg.Node)
	if err != nil {
		return nil, err
	}
	var out []runtime.Status
	for _, g := range guests {
		if g.Template == 1 || !g.HasTag(TagEnvironment) {
			continue
		}
		out = append(out, runtime.Status{Ref: refFor(g.VMID), EnvironmentID: environmentID(g), Running: g.Status == "running"})
	}
	return out, nil
}

// Capacity implements runtime.Runtime.
func (r *Runtime) Capacity(ctx context.Context) (runtime.Capacity, error) {
	ns, err := r.client.NodeStatus(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Capacity{}, err
	}
	available := ns.Memory.Available
	if available == 0 {
		available = ns.Memory.Free
	}
	c := runtime.Capacity{HostMemoryTotalMB: int(ns.Memory.Total >> 20), HostMemoryAvailableMB: int(available >> 20)}

	pools, err := r.client.ThinPools(ctx, r.cfg.Node)
	if err != nil {
		return runtime.Capacity{}, err
	}
	found := false
	for _, p := range pools {
		if p.LV != r.cfg.ThinPool {
			continue
		}
		found = true
		c.ThinPoolPercent = max(percent(p.Used, p.Size), percent(p.MetadataUsed, p.MetadataSize))
	}
	if !found {
		return runtime.Capacity{}, fmt.Errorf("proxmoxlxc: thin pool %q not found on node %s", r.cfg.ThinPool, r.cfg.Node)
	}

	envs, err := r.List(ctx)
	if err != nil {
		return runtime.Capacity{}, err
	}
	c.Environments = len(envs)
	return c, nil
}

func percent(used, size int64) float64 {
	if size <= 0 {
		return 0
	}
	return float64(used) * 100 / float64(size)
}

func mapNotFound(err error) error {
	if errors.Is(err, proxmox.ErrNotFound) {
		return fmt.Errorf("%w: %v", runtime.ErrNotFound, err)
	}
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/runtime/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/proxmoxlxc
git commit -m "feat(runtime): add the proxmox-lxc runtime"
```

---

### Task 8: `ghrm smoke`, example configuration and verification on a real host

**Files:**
- Create: `internal/ids/ids.go`
- Test: `internal/ids/ids_test.go`
- Create: `cmd/ghrm/smoke.go`
- Modify: `cmd/ghrm/main.go` (usage text and `switch`)
- Modify: `cmd/ghrm/main_test.go` (add smoke flag/config error tests)
- Create: `deploy/examples/ghrm.example.yaml`
- Create: `docs/development.md`

**Interfaces:**
- Consumes: `config.Load` (Task 2), `proxmox.New` (Task 6), `proxmoxlxc.New`/`Config` (Task 7), `runtime.EnvironmentSpec` (Task 5).
- Produces: `ids.NewEnvironmentID() string` (26-character lowercase ULID); the `ghrm smoke` command.

- [ ] **Step 1: Add the ULID dependency and write the failing tests**

Run: `go get github.com/oklog/ulid/v2@latest`

`internal/ids/ids_test.go`:

```go
package ids

import (
	"regexp"
	"testing"
)

func TestNewEnvironmentID(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-z]{26}$`)
	seen := map[string]bool{}
	for range 1000 {
		id := NewEnvironmentID()
		if !pattern.MatchString(id) {
			t.Fatalf("id %q is not a 26-char lowercase ULID", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}
```

Append to `cmd/ghrm/main_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ids/ ./cmd/ghrm/`
Expected: FAIL. `ids` does not build (`undefined: NewEnvironmentID`), and the smoke tests fail with exit code 2 and `unknown command "smoke"`.

- [ ] **Step 3: Write the implementation**

`internal/ids/ids.go`:

```go
// Package ids generates identifiers.
package ids

import (
	"strings"

	"github.com/oklog/ulid/v2"
)

// NewEnvironmentID returns a new, time-ordered, lowercase environment identifier.
// Lowercase keeps it valid in Proxmox tags and hostnames.
func NewEnvironmentID() string {
	return strings.ToLower(ulid.Make().String())
}
```

`cmd/ghrm/smoke.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/ids"
	"github.com/cocardoso/gh-runners-manager/internal/proxmox"
	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/runtime/proxmoxlxc"
)

// smoke creates, starts, inspects and destroys one environment to check the runtime end to end.
func smoke(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "/etc/ghrm/ghrm.yaml", "path to the configuration file")
	cores := fs.Int("cores", 1, "CPU cores for the environment")
	memory := fs.Int("memory", 1024, "memory limit in MB for the environment")
	timeout := fs.Duration("timeout", 3*time.Minute, "overall timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	p := cfg.Proxmox
	client, err := proxmox.New(proxmox.Config{URL: p.URL, TokenID: p.TokenID, TokenSecret: p.TokenSecret, InsecureSkipVerify: p.InsecureSkipVerify})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	rt := proxmoxlxc.New(client, proxmoxlxc.Config{
		Node: p.Node, TemplateVMID: p.TemplateVMID, Pool: p.Pool,
		VMIDStart: p.VMIDRange.Start, VMIDEnd: p.VMIDRange.End,
		ThinPool: p.ThinPool, FirewallSettle: p.FirewallSettle.Std(),
	})

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	start := time.Now()
	report := func(format string, a ...any) {
		fmt.Fprintf(stdout, "[%6.1fs] %s\n", time.Since(start).Seconds(), fmt.Sprintf(format, a...))
	}

	capacity, err := rt.Capacity(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "capacity:", err)
		return 1
	}
	report("host memory %d/%d MB available, thin pool %.1f%% used, %d ghrm environments",
		capacity.HostMemoryAvailableMB, capacity.HostMemoryTotalMB, capacity.ThinPoolPercent, capacity.Environments)

	id := ids.NewEnvironmentID()
	spec := runtime.EnvironmentSpec{
		ID: id, Hostname: "ghrm-smoke-" + id[len(id)-6:], Cores: *cores, MemoryMB: *memory,
		Env: map[string]string{"GHRM_SMOKE": "1", "GHRM_ENVIRONMENT_ID": id},
	}
	ref, err := rt.Create(ctx, spec)
	if err != nil {
		fmt.Fprintln(stderr, "create:", err)
		return 1
	}
	report("created environment %s as %s (includes the firewall settle)", id, ref)

	code := 0
	defer func() {
		dctx, dcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer dcancel()
		if err := rt.Destroy(dctx, ref); err != nil {
			fmt.Fprintln(stderr, "destroy:", err)
			return
		}
		report("destroyed %s", ref)
	}()

	if err := rt.Start(ctx, ref); err != nil {
		fmt.Fprintln(stderr, "start:", err)
		return 1
	}
	report("started %s", ref)

	for {
		st, err := rt.Status(ctx, ref)
		if err == nil && st.Running && st.IP != "" {
			report("running with IP %s (environment id %s)", st.IP, st.EnvironmentID)
			return code
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "waiting for an IP:", errors.Join(ctx.Err(), err))
			return 1
		case <-time.After(time.Second):
		}
	}
}
```

In `cmd/ghrm/main.go`, add the command to `usage`:

```go
const usage = `Usage: ghrm <command> [flags]

Commands:
  version   Print version information
  smoke     Create, start and destroy one environment to check the runtime
`
```

and add a case to the `switch` in `run`, before `default`:

```go
	case "smoke":
		return smoke(ctx, args[1:], stdout, stderr)
```

`deploy/examples/ghrm.example.yaml`:

```yaml
# Example ghrm configuration. Copy to /etc/ghrm/ghrm.yaml and adjust.
proxmox:
  url: https://pve.example.test:8006
  node: pve
  token_id: ghrm@pve!ghrm
  token_secret_file: /etc/ghrm/proxmox-token   # or set GHRM_PROXMOX_TOKEN_SECRET
  insecure_skip_verify: true                    # only for self-signed certificates
  template_vmid: 9000                           # must be outside vmid_range
  pool: ghrm
  vmid_range:
    start: 900
    end: 999
  thin_pool: data
  firewall_settle: 12s
```

- [ ] **Step 4: Run all tests**

Run: `make check`
Expected: vet clean, all tests PASS, `bin/ghrm` built.

- [ ] **Step 5: Write the development guide**

`docs/development.md`:

````markdown
# Development

## Build and test

    make check

## Scoped Proxmox API token

ghrm never uses `root`. Create a dedicated user, role, pool and token on the Proxmox host. Replace the template VMID, storage, SDN zone and node names with yours:

    pveum pool add ghrm --comment "gh-runners-manager environments"
    pveum role add GhrmRuntime --privs "VM.Allocate VM.Clone VM.Audit VM.PowerMgmt VM.Config.CPU VM.Config.Memory VM.Config.Disk VM.Config.Network VM.Config.Options Datastore.AllocateSpace Datastore.Audit SDN.Use Sys.Audit Pool.Audit"
    pveum user add ghrm@pve --comment "gh-runners-manager"
    pveum acl modify /pool/ghrm --users ghrm@pve --roles GhrmRuntime
    pveum acl modify /storage/local-lvm --users ghrm@pve --roles GhrmRuntime
    pveum acl modify /sdn/zones/runners --users ghrm@pve --roles GhrmRuntime
    pveum acl modify /nodes/pve --users ghrm@pve --roles GhrmRuntime
    pveum pool modify ghrm --vms 9000          # the template must be in the pool
    pveum user token add ghrm@pve ghrm --privsep 0

Store the printed secret in `/etc/ghrm/proxmox-token` (mode `0600`) or export it as `GHRM_PROXMOX_TOKEN_SECRET`.

## Smoke test against a real host

The template must be an LXC template (`pct template <vmid>`) attached to the job network, with its firewall enabled and the job security group applied.

    make build
    ./bin/ghrm smoke --config ghrm.yaml

Expected output: the host capacity, then creation (about 12–15 s including the firewall settle), start, a running IP on the job network, and destruction.
````

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/ids cmd/ghrm deploy docs/development.md
git commit -m "feat: add ghrm smoke to exercise the Proxmox runtime end to end"
```

- [ ] **Step 7: Verify on a real Proxmox host (requires operator approval)**

This step changes Proxmox users and ACLs, so it needs explicit approval from the operator before it runs.

1. Run the `pveum` commands from `docs/development.md` with the host's real template VMID, storage, zone and node. The template VMID must be outside the configured `vmid_range`.
2. Write a local `ghrm.yaml` (ignored by git) from `deploy/examples/ghrm.example.yaml`.
3. Run `./bin/ghrm smoke --config ghrm.yaml`.

Expected:
- The capacity line is printed.
- The environment is created with the **scoped token**. This proves that setting the LXC `env` option does not need privileges beyond `GhrmRuntime`, which answers spec risk #2.
- The environment starts and gets an IP on the job network, then it is destroyed.
- Afterwards, `pct list` shows no leftover guest in the range.

If `env` is rejected with a permission error, record the exact message. M2 then switches to the fallback bootstrap (spec §14 item 2).

- [ ] **Step 8: Push and confirm CI**

```bash
git push origin main
gh run watch --exit-status
```

Expected: the `ci` workflow passes on GitHub-hosted runners.
