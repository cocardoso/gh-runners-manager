package api

import (
	"context"
	"crypto/sha256"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/github"
)

// targetsTTL is how long a credential's repositories are kept before asking GitHub again.
const targetsTTL = 5 * time.Minute

type targetsCache struct {
	mu      sync.Mutex
	entries map[string]targetsEntry
}

type targetsEntry struct {
	key     [32]byte // the token's hash: a replaced token is asked again
	at      time.Time
	targets []github.Target
}

func (c *targetsCache) get(name, token string, ttl time.Duration) ([]github.Target, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[name]
	if !ok || e.key != sha256.Sum256([]byte(token)) || time.Since(e.at) > ttl {
		return nil, false
	}
	return e.targets, true
}

func (c *targetsCache) put(name, token string, targets []github.Target) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]targetsEntry{}
	}
	c.entries[name] = targetsEntry{key: sha256.Sum256([]byte(token)), at: time.Now(), targets: targets}
}

// registerCredentialTargets lets the UI pick a repository or organization a credential
// can reach, and check a token before it is saved.
func registerCredentialTargets(a huma.API, d Deps) {
	tags := []string{"settings"}
	cache := &targetsCache{}
	unavailable := func() error { return huma.Error409Conflict("GitHub credentials are read-only here") }
	type targetsIn struct {
		Name    string `path:"name"`
		Refresh bool   `query:"refresh" doc:"Ask GitHub again instead of using the list of the last minutes"`
	}
	type targetsOut struct {
		Body struct {
			Targets []github.Target `json:"targets"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "list-credential-targets", Method: http.MethodGet, Path: "/api/v1/credentials/{name}/targets",
		Summary: "The repositories and organizations a credential's token can reach", Tags: tags},
		func(ctx context.Context, in *targetsIn) (*targetsOut, error) {
			if d.Settings == nil || d.CredentialTargets == nil {
				return nil, unavailable()
			}
			c, ok := d.Settings.Credential(in.Name)
			if !ok {
				return nil, huma.Error404NotFound("credential not found")
			}
			out := &targetsOut{}
			if t, ok := cache.get(in.Name, c.Token, targetsTTL); ok && !in.Refresh {
				out.Body.Targets = t
				return out, nil
			}
			t, err := d.CredentialTargets(ctx, c.Token)
			if err != nil {
				return nil, huma.Error502BadGateway(err.Error())
			}
			cache.put(in.Name, c.Token, t)
			out.Body.Targets = t
			return out, nil
		})

	type checkIn struct {
		Body struct {
			Token string `json:"token" minLength:"1"`
		}
	}
	type checkOut struct {
		Body struct {
			OK            bool   `json:"ok"`
			Login         string `json:"login,omitempty"`
			Repositories  int    `json:"repositories"`
			Organizations int    `json:"organizations"`
			Error         string `json:"error,omitempty"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "check-token", Method: http.MethodPost, Path: "/api/v1/credentials/check",
		Summary: "Check a token before saving it: whose it is and what it can reach", Tags: tags},
		func(ctx context.Context, in *checkIn) (*checkOut, error) {
			if d.TestCredential == nil {
				return nil, unavailable()
			}
			out := &checkOut{}
			login, err := d.TestCredential(ctx, in.Body.Token)
			if err != nil {
				out.Body.Error = err.Error()
				return out, nil
			}
			out.Body.OK, out.Body.Login = true, login
			if d.CredentialTargets != nil {
				targets, err := d.CredentialTargets(ctx, in.Body.Token)
				if err != nil {
					out.Body.Error = err.Error()
				}
				for _, t := range targets {
					if t.Kind == "organization" {
						out.Body.Organizations++
					} else {
						out.Body.Repositories++
					}
				}
			}
			return out, nil
		})
}
