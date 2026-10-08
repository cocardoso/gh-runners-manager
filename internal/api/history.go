package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/retention"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// HistorySettings says whether history is cleaned every day and how long it is kept.
type HistorySettings retention.Settings

// CleanupResult is how much history a cleanup deleted (or would delete), with a warning
// when the rows went but their log files or the compaction did not.
type CleanupResult struct {
	store.HistoryCounts
	Warning string `json:"warning,omitempty"`
}

// registerHistory adds the history settings, cleanup and record deletion endpoints.
func registerHistory(a huma.API, d Deps) {
	tags := []string{"history"}
	unavailable := func() error { return huma.Error503ServiceUnavailable("history cleanup is not configured") }
	huma.Register(a, huma.Operation{OperationID: "get-history-settings", Method: http.MethodGet, Path: "/api/v1/history/settings",
		Summary: "How long history is kept, and whether it is cleaned every day", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body HistorySettings }, error) {
			if d.History == nil {
				return nil, unavailable()
			}
			s, err := d.History.Settings(ctx)
			return &struct{ Body HistorySettings }{HistorySettings(s)}, err
		})
	huma.Register(a, huma.Operation{OperationID: "put-history-settings", Method: http.MethodPut, Path: "/api/v1/history/settings",
		Summary: "Change the history settings", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct{ Body HistorySettings }) (*struct{}, error) {
			if d.History == nil {
				return nil, unavailable()
			}
			if err := d.History.PutSettings(ctx, retention.Settings(in.Body)); err != nil {
				if errors.Is(err, retention.ErrInvalid) {
					return nil, huma.Error422UnprocessableEntity(err.Error())
				}
				return nil, err
			}
			audit(ctx, d, "history_settings", fmt.Sprintf("history settings changed by %s: %s, %d days, audit %d days", Actor(ctx), in.Body.Mode, in.Body.Days, in.Body.AuditDays),
				events.Refs{}, map[string]any{"mode": in.Body.Mode, "days": in.Body.Days, "audit_days": in.Body.AuditDays})
			return &struct{}{}, nil
		})
	type cleanupIn struct {
		Body struct {
			Before      time.Time  `json:"before" doc:"Delete history older than this"`
			AuditBefore *time.Time `json:"audit_before,omitempty" doc:"Delete audit events older than this (default: the audit retention, never later than before)"`
			DryRun      bool       `json:"dry_run,omitempty" doc:"Only count"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "cleanup-history", Method: http.MethodPost, Path: "/api/v1/history/cleanup",
		Summary: "Delete (or count) history older than a date", Tags: tags},
		func(ctx context.Context, in *cleanupIn) (*struct{ Body CleanupResult }, error) {
			if d.History == nil {
				return nil, unavailable()
			}
			if !in.Body.Before.Before(time.Now()) {
				return nil, huma.Error422UnprocessableEntity("before must be in the past")
			}
			_, audit0, err := d.History.Cutoffs(ctx)
			if err != nil {
				return nil, err
			}
			if in.Body.AuditBefore != nil {
				audit0 = *in.Body.AuditBefore
			}
			if in.Body.DryRun {
				c, err := d.History.Preview(ctx, in.Body.Before, audit0)
				return &struct{ Body CleanupResult }{CleanupResult{HistoryCounts: c}}, err
			}
			c, err := d.History.Clean(ctx, in.Body.Before, audit0)
			if err != nil && !errors.Is(err, retention.ErrPartial) {
				return nil, err
			}
			// The rows are gone once Clean gets past the delete: say so, and audit it.
			out := CleanupResult{HistoryCounts: c}
			if err != nil {
				out.Warning = err.Error()
			}
			audit(ctx, d, "history_cleanup", fmt.Sprintf("history before %s deleted by %s: %s", in.Body.Before.Format(time.RFC3339), Actor(ctx), retention.Describe(c)),
				events.Refs{}, map[string]any{"counts": c, "before": in.Body.Before})
			return &struct{ Body CleanupResult }{out}, nil
		})
	type idPath struct {
		ID string `path:"id"`
	}
	recordError := func(err error) error {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return huma.Error404NotFound("not found")
		case errors.Is(err, store.ErrConflict):
			return huma.Error409Conflict(strings.TrimPrefix(err.Error(), store.ErrConflict.Error()+": "))
		}
		return err
	}
	huma.Register(a, huma.Operation{OperationID: "delete-template-record", Method: http.MethodDelete, Path: "/api/v1/templates/{id}",
		Summary: "Delete a failed or deleted template's record", Tags: []string{"templates"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := d.Store.DeleteTemplateRecord(ctx, in.ID); err != nil {
				return nil, recordError(err)
			}
			audit(ctx, d, "template_delete", "template record "+in.ID+" deleted by "+Actor(ctx), events.Refs{}, map[string]any{"template": in.ID})
			return &struct{}{}, nil
		})
	huma.Register(a, huma.Operation{OperationID: "delete-environment-record", Method: http.MethodDelete, Path: "/api/v1/environments/{id}",
		Summary: "Delete a destroyed environment with its jobs, events and logs", Tags: []string{"environments"}, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idPath) (*struct{}, error) {
			if err := d.Store.DeleteEnvironmentHistory(ctx, in.ID); err != nil {
				return nil, recordError(err)
			}
			if d.Logs != nil {
				_ = d.Logs.RemoveEnvironment(in.ID)
			}
			audit(ctx, d, "environment_delete", "environment "+in.ID+" deleted from history by "+Actor(ctx), events.Refs{}, map[string]any{"environment": in.ID})
			return &struct{}{}, nil
		})
}
