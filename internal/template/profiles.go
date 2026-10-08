package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cocardoso/gh-runners-manager/internal/runtime"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// ErrNoProfile is returned for a profile that does not exist.
var ErrNoProfile = errors.New("template: no such profile")

// ErrDefaultProfile is returned when deleting the default profile.
var ErrDefaultProfile = errors.New("template: the default profile cannot be deleted")

// Profiles returns every profile: the default one first (stored, or DefaultProfile), then
// the others by name.
func (s *Service) Profiles(ctx context.Context) ([]Profile, error) {
	stored, err := s.d.Store.ListTemplateProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := []Profile{DefaultProfile()}
	for _, sp := range stored {
		p, err := decodeProfile(sp)
		if err != nil {
			continue
		}
		if p.Name == store.DefaultProfile {
			out[0] = p
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Profile returns one profile; the default one always exists.
func (s *Service) Profile(ctx context.Context, name string) (Profile, error) {
	sp, err := s.d.Store.GetTemplateProfile(ctx, name)
	switch {
	case errors.Is(err, store.ErrNotFound) && name == store.DefaultProfile:
		return DefaultProfile(), nil
	case errors.Is(err, store.ErrNotFound):
		return Profile{}, fmt.Errorf("%w: %q", ErrNoProfile, name)
	case err != nil:
		return Profile{}, err
	}
	return decodeProfile(sp)
}

func decodeProfile(sp store.TemplateProfile) (Profile, error) {
	var p Profile
	if err := json.Unmarshal(sp.Spec, &p); err != nil {
		return Profile{}, fmt.Errorf("template: profile %s: %w", sp.Name, err)
	}
	p.Name = sp.Name
	return p.Normalize(), nil
}

// PutProfile creates or changes a profile. A change rebuilds its templates on the next check.
func (s *Service) PutProfile(ctx context.Context, p Profile) error {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := s.d.Store.PutTemplateProfile(ctx, p.Name, b); err != nil {
		return err
	}
	s.recheck.Store(true) // check (and build) on the next tick
	return nil
}

// DeleteProfile removes a profile and retires its versions; the caller makes sure no scale
// set uses it. The versions are deleted once no environment depends on them.
func (s *Service) DeleteProfile(ctx context.Context, name string) error {
	if name == store.DefaultProfile {
		return ErrDefaultProfile
	}
	if err := s.d.Store.DeleteTemplateProfile(ctx, name); errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: %q", ErrNoProfile, name)
	} else if err != nil {
		return err
	}
	list, err := s.d.Store.ListTemplates(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for _, t := range list {
		if t.Profile != name || (t.State != store.TemplateReady && t.State != store.TemplateActive) {
			continue
		}
		t.State = store.TemplateRetired
		_ = s.d.Store.UpdateTemplate(ctx, t)
	}
	s.mu.Unlock()
	s.retain(ctx)
	return nil
}

// Active implements controller.TemplateSource.
func (s *Service) Active(ctx context.Context, profile string) (string, int) {
	ref, vmid, _ := s.ActiveFirewallGated(ctx, profile)
	return ref, vmid
}

// ActiveFirewallGated returns the template a scale set of the profile clones, and whether
// its agent waits for the job network's firewall (its verification reported the feature
// and found the probe dropped). A profile with no active version yet clones the default
// profile's; the bootstrap template, never verified, never gates.
func (s *Service) ActiveFirewallGated(ctx context.Context, profile string) (string, int, bool) {
	t, err := s.d.Store.ActiveTemplate(ctx, profileOrDefault(profile))
	if err != nil && profileOrDefault(profile) != store.DefaultProfile {
		t, err = s.d.Store.ActiveTemplate(ctx, store.DefaultProfile)
	}
	if err != nil || t.RuntimeRef == "" {
		return "", s.d.BootstrapVMID, false
	}
	return s.d.Runtime.TemplateEnvironmentRef(runtime.TemplateRef{ID: t.RuntimeRef}), t.VMID, t.FirewallGate
}

func profileOrDefault(name string) string {
	if name == "" {
		return store.DefaultProfile
	}
	return name
}
