package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// UpdateSelf replaces the agent at exe with the control plane's when the build spec
// announces another one. A builder is a clone of the active template, whose agent may
// predate fixes to the build itself. It reports whether exe was replaced; the caller
// then re-executes it.
func UpdateSelf(ctx context.Context, c *Client, exe string) (bool, error) {
	var spec ingest.BuildSpec
	if err := c.GetJSON(ctx, ingest.BuildSpecPath, &spec); err != nil {
		return false, err
	}
	if spec.AgentSHA256 == "" {
		return false, nil
	}
	if cur, err := fileSHA256(exe); err == nil && cur == spec.AgentSHA256 {
		return false, nil
	}
	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp) // no-op after the rename
	h := sha256.New()
	err = c.Download(ctx, ingest.BuildAgentPath, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, fmt.Errorf("agent: download the control plane's agent: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != spec.AgentSHA256 {
		return false, fmt.Errorf("agent: downloaded agent has SHA-256 %s, want %s", got, spec.AgentSHA256)
	}
	if err := os.Rename(tmp, exe); err != nil {
		return false, err
	}
	return true, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// UpdateAndRestart replaces a stale agent (UpdateSelf) and calls restart, which
// re-executes it and returns only on failure. It returns nil when the agent was already
// current; any error means the caller keeps going with the running agent.
func UpdateAndRestart(ctx context.Context, c *Client, exe string, restart func() error) error {
	updated, err := UpdateSelf(ctx, c, exe)
	if err != nil {
		return fmt.Errorf("could not update to the control plane's agent: %w", err)
	}
	if !updated {
		return nil
	}
	c.Log("agent", "updated to the control plane's agent; restarting it")
	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_ = c.Flush(fctx)
	cancel()
	if err := restart(); err != nil {
		return fmt.Errorf("could not restart the updated agent: %w", err)
	}
	return nil
}
