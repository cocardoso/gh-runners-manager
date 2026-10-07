package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/internal/template"
)

// TemplateService is what the API needs from the template service.
type TemplateService interface {
	Build(ctx context.Context, trigger string) (store.Template, error)
	Activate(ctx context.Context, id string) error
	Pin(ctx context.Context, id string, pinned bool) error
	Running(ctx context.Context) bool
	InUse(ctx context.Context, t store.Template) bool
	Enabled() bool
}

// Template is the API view of a template version.
type Template struct {
	ID            string          `json:"id"`
	SlimRelease   string          `json:"slim_release,omitempty"`
	RunnerVersion string          `json:"runner_version,omitempty"`
	LayerVersion  string          `json:"layer_version,omitempty"`
	State         string          `json:"state"`
	VMID          int             `json:"vmid"`
	ArchiveSHA256 string          `json:"archive_sha256,omitempty"`
	SizeBytes     int64           `json:"size_bytes"`
	Pinned        bool            `json:"pinned"`
	Active        bool            `json:"active"`
	Bootstrap     bool            `json:"bootstrap"`
	InUse         bool            `json:"in_use"`
	Trigger       string          `json:"trigger,omitempty"`
	BuildEnvID    string          `json:"build_environment_id,omitempty"`
	VerifyEnvID   string          `json:"verify_environment_id,omitempty"`
	FailureStage  string          `json:"failure_stage,omitempty"`
	FailureReason string          `json:"failure_reason,omitempty"`
	Report        json.RawMessage `json:"report,omitempty" doc:"Fidelity report: self-test checks and software report differences"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	ActivatedAt   time.Time       `json:"activated_at"`
}

func (d Deps) toTemplate(ctx context.Context, t store.Template) Template {
	out := Template{ID: t.ID, SlimRelease: t.SlimRelease, RunnerVersion: t.RunnerVersion, LayerVersion: t.LayerVersion, State: t.State,
		VMID: t.VMID, ArchiveSHA256: t.ArchiveSHA256, SizeBytes: t.SizeBytes, Pinned: t.Pinned, Active: t.State == store.TemplateActive,
		Bootstrap: t.Trigger == "bootstrap", Trigger: t.Trigger, BuildEnvID: t.BuildEnvID, VerifyEnvID: t.VerifyEnvID,
		FailureStage: t.FailureStage, FailureReason: t.FailureReason, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, ActivatedAt: t.ActivatedAt}
	if json.Valid(t.Report) {
		out.Report = t.Report
	}
	if d.Templates != nil && (t.State == store.TemplateActive || t.State == store.TemplateReady || t.State == store.TemplateRetired) {
		out.InUse = d.Templates.InUse(ctx, t)
	}
	return out
}

// requireAdmin checks the bearer admin token of a mutating call.
func requireAdmin(d Deps, authorization string) error {
	if d.AdminToken == "" {
		return huma.Error403Forbidden("mutating calls are disabled: no admin token is configured")
	}
	token, _ := strings.CutPrefix(authorization, "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(d.AdminToken)) != 1 {
		return huma.Error401Unauthorized("invalid admin token")
	}
	return nil
}

func templateError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("template not found")
	case errors.Is(err, template.ErrBuildRunning), errors.Is(err, template.ErrNotReady):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, template.ErrDisabled):
		return huma.Error409Conflict(err.Error() + " (set templates.vmid_range)")
	}
	return huma.Error502BadGateway(err.Error())
}

func registerTemplates(a huma.API, d Deps) {
	type listOut struct {
		Body struct {
			Templates []Template `json:"templates"`
			Building  bool       `json:"building"`
			Enabled   bool       `json:"enabled"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "list-templates", Method: http.MethodGet, Path: "/api/v1/templates", Summary: "List template versions, newest first", Tags: []string{"templates"}},
		func(ctx context.Context, _ *struct{}) (*listOut, error) {
			list, err := d.Store.ListTemplates(ctx)
			if err != nil {
				return nil, err
			}
			out := &listOut{}
			out.Body.Templates = make([]Template, 0, len(list))
			for _, t := range list {
				out.Body.Templates = append(out.Body.Templates, d.toTemplate(ctx, t))
			}
			if d.Templates != nil {
				out.Body.Building, out.Body.Enabled = d.Templates.Running(ctx), d.Templates.Enabled()
			}
			return out, nil
		})

	type idIn struct {
		ID string `path:"id"`
	}
	huma.Register(a, huma.Operation{OperationID: "get-template", Method: http.MethodGet, Path: "/api/v1/templates/{id}", Summary: "Get a template version", Tags: []string{"templates"}},
		func(ctx context.Context, in *idIn) (*struct{ Body Template }, error) {
			t, err := d.Store.GetTemplate(ctx, in.ID)
			if err != nil {
				return nil, templateError(err)
			}
			return &struct{ Body Template }{Body: d.toTemplate(ctx, t)}, nil
		})

	type adminIn struct {
		Authorization string `header:"Authorization"`
	}
	huma.Register(a, huma.Operation{OperationID: "build-template", Method: http.MethodPost, Path: "/api/v1/templates/build",
		Summary: "Build a new template version (admin)", Tags: []string{"templates"}, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *adminIn) (*struct{ Body Template }, error) {
			if err := requireAdmin(d, in.Authorization); err != nil {
				return nil, err
			}
			if d.Templates == nil {
				return nil, templateError(template.ErrDisabled)
			}
			t, err := d.Templates.Build(context.WithoutCancel(ctx), "manual")
			if err != nil {
				return nil, templateError(err)
			}
			return &struct{ Body Template }{Body: d.toTemplate(ctx, t)}, nil
		})

	type idAdminIn struct {
		ID            string `path:"id"`
		Authorization string `header:"Authorization"`
	}
	huma.Register(a, huma.Operation{OperationID: "activate-template", Method: http.MethodPost, Path: "/api/v1/templates/{id}/activate",
		Summary: "Activate a ready version, or roll back to it (admin)", Tags: []string{"templates"}, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *idAdminIn) (*struct{}, error) {
			if err := requireAdmin(d, in.Authorization); err != nil {
				return nil, err
			}
			if d.Templates == nil {
				return nil, templateError(template.ErrDisabled)
			}
			if err := d.Templates.Activate(ctx, in.ID); err != nil {
				return nil, templateError(err)
			}
			return &struct{}{}, nil
		})
	for _, pin := range []bool{true, false} {
		name := "unpin"
		if pin {
			name = "pin"
		}
		huma.Register(a, huma.Operation{OperationID: name + "-template", Method: http.MethodPost, Path: "/api/v1/templates/{id}/" + name,
			Summary: "Pin or unpin a version; a pinned version blocks automatic activation (admin)", Tags: []string{"templates"}, DefaultStatus: http.StatusNoContent},
			func(ctx context.Context, in *idAdminIn) (*struct{}, error) {
				if err := requireAdmin(d, in.Authorization); err != nil {
					return nil, err
				}
				if d.Templates == nil {
					return nil, templateError(template.ErrDisabled)
				}
				if err := d.Templates.Pin(ctx, in.ID, pin); err != nil {
					return nil, templateError(err)
				}
				return &struct{}{}, nil
			})
	}
}
