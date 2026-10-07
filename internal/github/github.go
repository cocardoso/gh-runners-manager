// Package github adapts the actions/scaleset client to the controller.
package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/version"
)

// Client talks to GitHub's Runner Scale Set API, one scaleset.Client per scale set.
type Client struct {
	cfg    *config.Config
	logger *slog.Logger

	mu      sync.Mutex
	clients map[string]*scaleset.Client
}

// New returns a Client.
func New(cfg *config.Config, logger *slog.Logger) *Client {
	return &Client{cfg: cfg, logger: logger, clients: map[string]*scaleset.Client{}}
}

func (c *Client) client(name string) (*scaleset.Client, config.ScaleSet, error) {
	var ss config.ScaleSet
	found := false
	for _, s := range c.cfg.ScaleSets {
		if s.Name == name {
			ss, found = s, true
		}
	}
	if !found {
		return nil, ss, fmt.Errorf("github: unknown scale set %q", name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.clients[name]; ok {
		return cl, ss, nil
	}
	cred, ok := c.cfg.Credential(ss.Credential)
	if !ok {
		return nil, ss, fmt.Errorf("github: scale set %s: unknown credential %q", name, ss.Credential)
	}
	cl, err := scaleset.NewClientWithPersonalAccessToken(scaleset.NewClientWithPersonalAccessTokenConfig{
		GitHubConfigURL:     ss.URL,
		PersonalAccessToken: cred.Token,
		SystemInfo: scaleset.SystemInfo{System: "gh-runners-manager", Subsystem: "controller",
			Version: version.Version, CommitSHA: version.Commit},
	})
	if err != nil {
		return nil, ss, fmt.Errorf("github: scale set %s: %w", name, err)
	}
	c.clients[name] = cl
	return cl, ss, nil
}

// EnsureScaleSet finds the scale set by name in its runner group, creating it when missing.
func (c *Client) EnsureScaleSet(ctx context.Context, cfg config.ScaleSet) (int, error) {
	cl, _, err := c.client(cfg.Name)
	if err != nil {
		return 0, err
	}
	groupID := 1
	if cfg.RunnerGroup != scaleset.DefaultRunnerGroup {
		g, err := cl.GetRunnerGroupByName(ctx, cfg.RunnerGroup)
		if err != nil {
			return 0, fmt.Errorf("github: runner group %q: %w", cfg.RunnerGroup, err)
		}
		groupID = g.ID
	}
	labels := []scaleset.Label{{Name: cfg.Name}}
	for _, l := range cfg.Labels {
		if l = strings.TrimSpace(l); l != "" && l != cfg.Name {
			labels = append(labels, scaleset.Label{Name: l})
		}
	}
	existing, err := cl.GetRunnerScaleSet(ctx, groupID, cfg.Name)
	if err != nil {
		return 0, fmt.Errorf("github: get scale set %s: %w", cfg.Name, err)
	}
	if existing != nil {
		cl.SetSystemInfo(systemInfo(existing.ID))
		return existing.ID, nil
	}
	created, err := cl.CreateRunnerScaleSet(ctx, &scaleset.RunnerScaleSet{
		Name: cfg.Name, RunnerGroupID: groupID, Labels: labels,
		RunnerSetting: scaleset.RunnerSetting{DisableUpdate: true},
	})
	if err != nil {
		return 0, fmt.Errorf("github: create scale set %s: %w", cfg.Name, err)
	}
	cl.SetSystemInfo(systemInfo(created.ID))
	return created.ID, nil
}

func systemInfo(id int) scaleset.SystemInfo {
	return scaleset.SystemInfo{System: "gh-runners-manager", Subsystem: "controller",
		Version: version.Version, CommitSHA: version.Commit, ScaleSetID: id}
}

// GenerateJIT creates a just-in-time runner registration.
func (c *Client) GenerateJIT(ctx context.Context, scaleSet string, scaleSetID int, runnerName string) (int64, string, error) {
	cl, _, err := c.client(scaleSet)
	if err != nil {
		return 0, "", err
	}
	jit, err := cl.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{Name: runnerName, WorkFolder: "_work"}, scaleSetID)
	if err != nil {
		return 0, "", fmt.Errorf("github: JIT config for %s: %w", runnerName, err)
	}
	var id int64
	if jit.Runner != nil {
		id = int64(jit.Runner.ID)
	}
	return id, jit.EncodedJITConfig, nil
}

// RemoveRunner deregisters a runner. A runner that is already gone is not an error.
func (c *Client) RemoveRunner(ctx context.Context, scaleSet string, runnerID int64) error {
	cl, _, err := c.client(scaleSet)
	if err != nil {
		return err
	}
	err = cl.RemoveRunner(ctx, runnerID)
	if err == nil || errors.Is(err, scaleset.RunnerNotFoundError) || strings.Contains(err.Error(), "404") {
		return nil
	}
	return err
}

// Listen runs the scale set message listener until ctx ends or it fails.
func (c *Client) Listen(ctx context.Context, scaleSet string, scaleSetID, maxRunners int, s listener.Scaler) error {
	cl, _, err := c.client(scaleSet)
	if err != nil {
		return err
	}
	owner, _ := os.Hostname()
	if owner == "" {
		owner = "ghrm"
	}
	session, err := cl.MessageSessionClient(ctx, scaleSetID, owner)
	if err != nil {
		return fmt.Errorf("github: message session for %s: %w", scaleSet, err)
	}
	defer func() { _ = session.Close(context.WithoutCancel(ctx)) }()
	var lc listener.Client = session
	if h, ok := s.(AvailableJobHandler); ok {
		lc = withAvailableJobs(session, h, c.logger.With("scale_set", scaleSet))
	}
	l, err := listener.New(lc, listener.Config{ScaleSetID: scaleSetID, MaxRunners: maxRunners, Logger: c.logger.With("scale_set", scaleSet)})
	if err != nil {
		return err
	}
	return l.Run(ctx, s)
}
