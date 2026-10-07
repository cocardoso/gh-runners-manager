package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/cocardoso/gh-runners-manager/internal/auth"
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
