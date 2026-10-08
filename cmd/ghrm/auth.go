package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/cocardoso/gh-runners-manager/internal/auth"
	"github.com/cocardoso/gh-runners-manager/internal/github"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// newAuth returns the sign-in service. Until the admin account exists, a one-time setup
// token (dataDir/setup-token) is required to create it, so nobody else on the network
// can claim a fresh install first.
func newAuth(ctx context.Context, db *store.Store, dataDir string, logger *slog.Logger) (*auth.Service, error) {
	a := &auth.Service{Store: db}
	need, err := a.NeedsSetup(ctx)
	if err != nil || !need {
		return a, err
	}
	tok, err := auth.LoadOrCreateSetupToken(dataDir)
	if err != nil {
		return nil, err
	}
	a.SetupToken, a.SetupTokenFile = tok, filepath.Join(dataDir, "setup-token")
	logger.Warn("no admin account yet: open the web UI and create it with the setup token",
		"setup_token_file", a.SetupTokenFile, "setup_token", tok)
	return a, nil
}

// demoPassword is the demo's admin password (the demo serves simulated data only).
const demoPassword = "demo-password"

// demoAuth returns a sign-in service with the demo account admin / demoPassword.
func demoAuth(ctx context.Context, db *store.Store) (*auth.Service, error) {
	a := &auth.Service{Store: db, SetupToken: "demo"}
	need, err := a.NeedsSetup(ctx)
	if err != nil || !need {
		return a, err
	}
	_, err = a.Setup(ctx, "demo", "admin", demoPassword)
	return a, err
}

// demoTestCredential accepts tokens that look like fine-grained PATs (the demo never
// calls GitHub).
func demoTestCredential(_ context.Context, token string) (string, error) {
	if strings.HasPrefix(token, "github_pat_") {
		return "demo-user", nil
	}
	return "", errors.New("Bad credentials (the demo accepts tokens starting with github_pat_)")
}

// demoTargets answers like GitHub for a demo token: one organization and its repositories.
func demoTargets(ctx context.Context, token string) (github.TargetList, error) {
	if _, err := demoTestCredential(ctx, token); err != nil {
		return github.TargetList{}, err
	}
	t := []github.Target{{Kind: "organization", Owner: "octo", FullName: "octo", URL: "https://github.com/octo"}}
	for _, name := range []string{"api-service", "infra", "mobile", "web-app"} {
		t = append(t, github.Target{Kind: "repository", Owner: "octo", Name: name, FullName: "octo/" + name, URL: "https://github.com/octo/" + name, Private: name != "web-app"})
	}
	return github.TargetList{Targets: t}, nil
}
