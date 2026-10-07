// Package agent implements ghrm-agent, which runs inside every job environment:
// it starts the GitHub runner from the injected JIT config and streams logs,
// lifecycle events and metrics to the control plane (spec §4.3).
package agent

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// Bootstrap is what the control plane injected into the environment.
type Bootstrap struct {
	JITConfig     string
	EnvironmentID string
	URL           string
	Token         string
	Fingerprint   string
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
	}
	if b.JITConfig == "" {
		return b, false, nil
	}
	if b.URL == "" || b.Token == "" || b.Fingerprint == "" {
		return b, false, fmt.Errorf("agent: incomplete bootstrap: ingest URL, token and fingerprint are required")
	}
	return b, true, nil
}
