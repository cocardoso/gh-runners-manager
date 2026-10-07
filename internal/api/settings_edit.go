package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/settings"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// CredentialView is a GitHub credential without its token.
type CredentialView struct {
	Name      string   `json:"name"`
	Source    string   `json:"source" enum:"file,ui"`
	UsedBy    []string `json:"used_by"`
	TokenHint string   `json:"token_hint" doc:"The token's last four characters"`
}

// settingsError maps registry errors; anything else is a validation error.
func settingsError(err error) error {
	switch {
	case errors.Is(err, settings.ErrReadOnly), errors.Is(err, settings.ErrInUse):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("not found")
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

func hint(token string) string {
	if len(token) <= 4 {
		return ""
	}
	return token[len(token)-4:]
}

func registerSettingsEdit(a huma.API, d Deps) {
	tags := []string{"settings"}
	readOnly := func() error { return huma.Error409Conflict("settings are read-only here") }

	huma.Register(a, huma.Operation{OperationID: "list-credentials", Method: http.MethodGet, Path: "/api/v1/credentials",
		Summary: "List GitHub credentials (never their tokens)", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct {
			Body struct {
				Credentials []CredentialView `json:"credentials"`
			}
		}, error) {
			out := &struct {
				Body struct {
					Credentials []CredentialView `json:"credentials"`
				}
			}{}
			out.Body.Credentials = []CredentialView{}
			if d.Settings == nil {
				return out, nil
			}
			for _, c := range d.Settings.Credentials() {
				used := d.Settings.CredentialUsers(c.Name)
				if used == nil {
					used = []string{}
				}
				out.Body.Credentials = append(out.Body.Credentials, CredentialView{Name: c.Name, Source: c.Source, UsedBy: used, TokenHint: hint(c.Token)})
			}
			return out, nil
		})

	type putIn struct {
		Name string `path:"name"`
		Body struct {
			Token string `json:"token" minLength:"1" doc:"A fine-grained PAT (Administration: read and write; Actions: read for job steps)"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "put-credential", Method: http.MethodPut, Path: "/api/v1/credentials/{name}",
		Summary: "Create a GitHub credential or replace its token", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *putIn) (*struct{}, error) {
			if d.Settings == nil {
				return nil, readOnly()
			}
			if err := d.Settings.PutCredential(ctx, in.Name, in.Body.Token); err != nil {
				return nil, settingsError(err)
			}
			audit(ctx, d, "credential_put", "credential "+in.Name+" saved by "+Actor(ctx), events.Refs{}, map[string]any{"credential": in.Name})
			return &struct{}{}, nil
		})

	type nameIn struct {
		Name string `path:"name"`
	}
	huma.Register(a, huma.Operation{OperationID: "delete-credential", Method: http.MethodDelete, Path: "/api/v1/credentials/{name}",
		Summary: "Delete a GitHub credential that no scale set uses", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *nameIn) (*struct{}, error) {
			if d.Settings == nil {
				return nil, readOnly()
			}
			if err := d.Settings.DeleteCredential(ctx, in.Name); err != nil {
				return nil, settingsError(err)
			}
			audit(ctx, d, "credential_delete", "credential "+in.Name+" deleted by "+Actor(ctx), events.Refs{}, map[string]any{"credential": in.Name})
			return &struct{}{}, nil
		})

	type testOut struct {
		Body struct {
			OK    bool   `json:"ok"`
			Login string `json:"login,omitempty"`
			Error string `json:"error,omitempty"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "test-credential", Method: http.MethodPost, Path: "/api/v1/credentials/{name}/test",
		Summary: "Check that a credential's token works", Tags: tags},
		func(ctx context.Context, in *nameIn) (*testOut, error) {
			if d.Settings == nil || d.TestCredential == nil {
				return nil, readOnly()
			}
			c, ok := d.Settings.Credential(in.Name)
			if !ok {
				return nil, huma.Error404NotFound("credential not found")
			}
			out := &testOut{}
			login, err := d.TestCredential(ctx, c.Token)
			if err != nil {
				out.Body.Error = err.Error()
			} else {
				out.Body.OK, out.Body.Login = true, login
			}
			return out, nil
		})
}
