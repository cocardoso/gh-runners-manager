package api

import (
	"context"
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/sync/singleflight"

	"github.com/cocardoso/gh-runners-manager/internal/github"
)

// targetsTTL is how long a credential's repositories are kept before asking GitHub again.
const targetsTTL = 5 * time.Minute

// targetsRefreshMin is how fresh a list may be for a refresh to reuse it, so repeated
// refreshes do not spend the credential's GitHub rate limit.
var targetsRefreshMin = 10 * time.Second

type targetsCache struct {
	mu      sync.Mutex
	entries map[string]targetsEntry
	flight  singleflight.Group // two dialogs opened at once page through GitHub once
}

type targetsEntry struct {
	key     [32]byte // the token's hash: a replaced token is asked again
	at      time.Time
	targets github.TargetList
}

func (c *targetsCache) get(name, token string, ttl time.Duration) (github.TargetList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[name]
	if !ok || e.key != sha256.Sum256([]byte(token)) || time.Since(e.at) > ttl {
		delete(c.entries, name)
		return github.TargetList{}, false
	}
	return e.targets, true
}

// forget drops a removed credential's list.
func (c *targetsCache) forget(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, name)
}

func (c *targetsCache) put(name, token string, targets github.TargetList) {
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
		Refresh bool   `query:"refresh" doc:"Bypass the 5-minute cache and ask GitHub again (a list under 10 seconds old is reused)"`
	}
	type targetsOut struct {
		Body github.TargetList
	}
	huma.Register(a, huma.Operation{OperationID: "list-credential-targets", Method: http.MethodGet, Path: "/api/v1/credentials/{name}/targets",
		Summary: "The repositories and organizations a credential's token can reach", Tags: tags},
		func(ctx context.Context, in *targetsIn) (*targetsOut, error) {
			if d.Settings == nil || d.CredentialTargets == nil {
				return nil, unavailable()
			}
			c, ok := d.Settings.Credential(in.Name)
			if !ok {
				cache.forget(in.Name)
				return nil, huma.Error404NotFound("credential not found")
			}
			ttl := targetsTTL
			if in.Refresh {
				ttl = targetsRefreshMin
			}
			if t, ok := cache.get(in.Name, c.Token, ttl); ok {
				return &targetsOut{Body: t}, nil
			}
			v, err, _ := cache.flight.Do(in.Name, func() (any, error) {
				t, err := d.CredentialTargets(ctx, c.Token)
				if err == nil {
					cache.put(in.Name, c.Token, t)
				}
				return t, err
			})
			if err != nil {
				return nil, huma.Error502BadGateway(githubMessage(err))
			}
			return &targetsOut{Body: v.(github.TargetList)}, nil
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
			Truncated     bool   `json:"truncated" doc:"The token reaches more repositories than were counted"`
			Error         string `json:"error,omitempty" doc:"Why the token failed, or why its repositories could not be listed"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "check-token", Method: http.MethodPost, Path: "/api/v1/credentials/check",
		Summary: "Check a token before saving it: whose it is and what it can reach", Tags: tags},
		func(ctx context.Context, in *checkIn) (*checkOut, error) {
			if d.TestCredential == nil {
				return nil, huma.Error409Conflict("token checks are unavailable here")
			}
			out := &checkOut{}
			login, err := d.TestCredential(ctx, in.Body.Token)
			if err != nil {
				out.Body.Error = githubMessage(err)
				return out, nil
			}
			out.Body.OK, out.Body.Login = true, login
			if d.CredentialTargets != nil {
				list, err := d.CredentialTargets(ctx, in.Body.Token)
				if err != nil {
					out.Body.Error = githubMessage(err)
				}
				out.Body.Truncated = list.Truncated
				for _, t := range list.Targets {
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

// githubMessage is GitHub's own words, without the client's "github: " prefix.
func githubMessage(err error) string {
	return strings.TrimPrefix(err.Error(), "github: ")
}
