package api

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/store"
	"github.com/cocardoso/gh-runners-manager/internal/template"
)

// TemplateProfile is the API view of a template profile.
type TemplateProfile struct {
	Name      string              `json:"name"`
	Remove    []string            `json:"remove" doc:"Components of GitHub's recipe left out"`
	Toolcache map[string][]string `json:"toolcache" doc:"Versions preinstalled in the hosted tool cache, per tool"`
	Apt       []string            `json:"apt" doc:"Extra Ubuntu packages"`
	Script    string              `json:"script,omitempty" doc:"Run as root at the end of the build"`
	// ActiveTemplateID is the version scale sets of the profile clone ("" before its first build).
	ActiveTemplateID string   `json:"active_template_id,omitempty"`
	UsedBy           []string `json:"used_by" doc:"Scale sets that clone this profile's template"`
}

// TemplateProfileIn is a profile as saved.
type TemplateProfileIn struct {
	Remove    []string            `json:"remove,omitempty"`
	Toolcache map[string][]string `json:"toolcache,omitempty"`
	Apt       []string            `json:"apt,omitempty"`
	Script    string              `json:"script,omitempty" maxLength:"65536"`
}

// profileUsers lists the scale sets that use each profile.
func (d Deps) profileUsers() map[string][]string {
	out := map[string][]string{}
	add := func(name, profile string) {
		if profile == "" {
			profile = store.DefaultProfile
		}
		if !slices.Contains(out[profile], name) {
			out[profile] = append(out[profile], name)
		}
	}
	if d.Settings != nil {
		for _, ss := range d.Settings.ScaleSetConfigs() {
			add(ss.Name, ss.TemplateProfile)
		}
	} else if d.Config != nil {
		for _, ss := range d.Config.ScaleSets {
			add(ss.Name, ss.TemplateProfile)
		}
	}
	return out
}

func registerProfiles(a huma.API, d Deps) {
	tags := []string{"templates"}
	type listOut struct {
		Body struct {
			Profiles       []TemplateProfile    `json:"profiles"`
			Components     []template.Component `json:"components" doc:"What a profile can leave out of GitHub's recipe"`
			ToolcacheTools []string             `json:"toolcache_tools"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "list-template-profiles", Method: http.MethodGet, Path: "/api/v1/template-profiles",
		Summary: "List template profiles and what they can change", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*listOut, error) {
			out := &listOut{}
			out.Body.Components, out.Body.ToolcacheTools = template.Components, template.ToolcacheTools
			out.Body.Profiles = []TemplateProfile{}
			if d.Templates == nil {
				return out, nil
			}
			profiles, err := d.Templates.Profiles(ctx)
			if err != nil {
				return nil, err
			}
			users := d.profileUsers()
			for _, p := range profiles {
				v := TemplateProfile{Name: p.Name, Remove: nonNil(p.Remove), Toolcache: p.Toolcache, Apt: nonNil(p.Apt), Script: p.Script, UsedBy: nonNil(users[p.Name])}
				if v.Toolcache == nil {
					v.Toolcache = map[string][]string{}
				}
				if t, err := d.Store.ActiveTemplate(ctx, p.Name); err == nil {
					v.ActiveTemplateID = t.ID
				}
				out.Body.Profiles = append(out.Body.Profiles, v)
			}
			return out, nil
		})

	type putIn struct {
		Name string `path:"name"`
		Body TemplateProfileIn
	}
	huma.Register(a, huma.Operation{OperationID: "put-template-profile", Method: http.MethodPut, Path: "/api/v1/template-profiles/{name}",
		Summary: "Create or change a template profile; its templates are rebuilt (admin)", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *putIn) (*struct{}, error) {
			if d.Templates == nil {
				return nil, templateError(template.ErrDisabled)
			}
			p := template.Profile{Name: in.Name, Remove: in.Body.Remove, Toolcache: in.Body.Toolcache, Apt: in.Body.Apt, Script: in.Body.Script}
			if err := p.Normalize().Validate(); err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			if err := d.Templates.PutProfile(ctx, p); err != nil {
				return nil, err
			}
			audit(ctx, d, "template_profile_put", "template profile "+in.Name+" saved by "+Actor(ctx), events.Refs{}, map[string]any{"profile": in.Name})
			return &struct{}{}, nil
		})

	type nameIn struct {
		Name string `path:"name"`
	}
	huma.Register(a, huma.Operation{OperationID: "delete-template-profile", Method: http.MethodDelete, Path: "/api/v1/template-profiles/{name}",
		Summary: "Delete a template profile no scale set uses; its templates are retired (admin)", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *nameIn) (*struct{}, error) {
			if d.Templates == nil {
				return nil, templateError(template.ErrDisabled)
			}
			if users := d.profileUsers()[in.Name]; len(users) > 0 {
				return nil, huma.Error409Conflict("the template profile is used by " + joinNames(users))
			}
			err := d.Templates.DeleteProfile(ctx, in.Name)
			switch {
			case errors.Is(err, template.ErrDefaultProfile):
				return nil, huma.Error409Conflict(err.Error())
			case err != nil:
				return nil, templateError(err)
			}
			audit(ctx, d, "template_profile_delete", "template profile "+in.Name+" deleted by "+Actor(ctx), events.Refs{}, map[string]any{"profile": in.Name})
			return &struct{}{}, nil
		})
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

func joinNames(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}
