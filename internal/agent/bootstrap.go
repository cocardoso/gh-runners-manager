// Package agent implements ghrm-agent, which runs inside every job environment:
// it starts the GitHub runner from the injected JIT config and streams logs,
// lifecycle events and metrics to the control plane (spec §4.3).
package agent

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// Bootstrap is what the control plane injected into the environment.
type Bootstrap struct {
	JITConfig     string
	EnvironmentID string
	URL           string
	Token         string
	Fingerprint   string
	// Mode is ingest.ModeBuild or ingest.ModeSelfTest; empty for a job.
	Mode string
	// Blocked lists host:port addresses the self-test must find unreachable.
	Blocked []string
	// Mirrors lists the registry cache's host:port addresses the self-test must reach.
	Mirrors []string
	// FirewallProbe, when set, is checked before the runner starts (see WaitFirewall), with
	// FirewallSettle as the fallback delay.
	FirewallProbe  string
	FirewallSettle time.Duration
}

// ParseEnviron splits a NUL-separated environment block.
func ParseEnviron(b []byte) map[string]string {
	out := map[string]string{}
	for _, kv := range bytes.Split(b, []byte{0}) {
		if k, v, ok := strings.Cut(string(kv), "="); ok && k != "" {
			out[k] = v
		}
	}
	return out
}

// LoadBootstrap reads the bootstrap from an environ file (normally /proc/1/environ).
// ok is false when there is no JIT config, as when the template itself boots.
func LoadBootstrap(path string) (Bootstrap, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Bootstrap{}, false, fmt.Errorf("agent: read %s: %w", path, err)
	}
	env := ParseEnviron(raw)
	b := Bootstrap{
		JITConfig:     env[ingest.EnvJITConfig],
		EnvironmentID: env[ingest.EnvEnvironment],
		URL:           env[ingest.EnvURL],
		Token:         env[ingest.EnvToken],
		Fingerprint:   env[ingest.EnvFingerprint],
		Mode:          env[ingest.EnvMode],
		FirewallProbe: env[ingest.EnvFirewallProbe],
	}
	b.FirewallSettle = 12 * time.Second
	if d, err := time.ParseDuration(env[ingest.EnvFirewallSettle]); err == nil && d > 0 {
		b.FirewallSettle = d
	}
	for _, a := range strings.Split(env[ingest.EnvSelfTestBlocked], ",") {
		if a = strings.TrimSpace(a); a != "" {
			b.Blocked = append(b.Blocked, a)
		}
	}
	for _, a := range strings.Split(env[ingest.EnvSelfTestMirrors], ",") {
		if a = strings.TrimSpace(a); a != "" {
			b.Mirrors = append(b.Mirrors, a)
		}
	}
	if b.JITConfig == "" && b.Mode == "" {
		return b, false, nil
	}
	if b.URL == "" || b.Token == "" || b.Fingerprint == "" {
		return b, false, fmt.Errorf("agent: incomplete bootstrap: ingest URL, token and fingerprint are required")
	}
	return b, true, nil
}

// MergeEnvironmentFile adds the variables of an /etc/environment style file to env.
// The file wins over env, except LANG, which stays C.UTF-8 like the hosted images.
func MergeEnvironmentFile(env []string, path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return env
	}
	vars := map[string]string{}
	var order []string
	set := func(k, v string) {
		if _, ok := vars[k]; !ok {
			order = append(order, k)
		}
		vars[k] = v
	}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			set(k, v)
		}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok || line == "" || strings.HasPrefix(line, "#") || strings.ContainsAny(k, " \t") || k == "LANG" {
			continue
		}
		set(k, strings.Trim(v, `"'`))
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+vars[k])
	}
	return out
}
