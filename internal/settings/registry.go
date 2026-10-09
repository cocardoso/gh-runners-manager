// Package settings holds the effective GitHub credentials, scale sets and capacity limits:
// the ones in the configuration file (read-only) and the ones set in the UI (stored in the
// database, tokens sealed in the vault). Changes are announced to subscribers.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/secrets"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// Sources of a setting.
const (
	SourceFile = "file"
	SourceUI   = "ui"
	// CapacityDefault means neither the file nor the UI set the capacity limits.
	CapacityDefault = "default"
)

// capacityKey is the meta key of the capacity limits set in the UI.
const capacityKey = "capacity"

var (
	// ErrReadOnly means the setting comes from the configuration file.
	ErrReadOnly = errors.New("settings: defined in the configuration file; edit ghrm.yaml instead")
	// ErrInUse means a scale set still uses the credential.
	ErrInUse = errors.New("settings: the credential is used by a scale set")
	// ErrExists means a scale set of that name already exists.
	ErrExists = errors.New("settings: a scale set with this name already exists")
)

// Credential is a GitHub credential. Token is never serialized.
type Credential struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Token  string `json:"-"`
}

// ScaleSet is a scale set with its source.
type ScaleSet struct {
	config.ScaleSet
	Source string
}

// Registry is safe for concurrent use.
type Registry struct {
	cfg   *config.Config
	store *store.Store
	vault *secrets.Vault

	wmu     sync.Mutex // serializes changes, so checks and writes do not interleave
	mu      sync.RWMutex
	uiCreds map[string]Credential
	uiSets  map[string]config.ScaleSet
	uiCap   *config.Capacity
	// loadWarning says why the stored capacity limits were ignored ("" when they were not).
	loadWarning string
	subs        map[int]chan struct{}
	nextSub     int
}

// New loads the UI settings.
func New(ctx context.Context, cfg *config.Config, s *store.Store, v *secrets.Vault) (*Registry, error) {
	r := &Registry{cfg: cfg, store: s, vault: v, uiCreds: map[string]Credential{}, uiSets: map[string]config.ScaleSet{}, subs: map[int]chan struct{}{}}
	creds, err := s.ListCredentialRecords(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range creds {
		tok, _, err := v.Get(ctx, config.VaultGitHubPrefix+c.Name)
		if err != nil {
			return nil, err
		}
		r.uiCreds[c.Name] = Credential{Name: c.Name, Source: SourceUI, Token: tok}
	}
	sets, err := s.ListScaleSetConfigs(ctx)
	if err != nil {
		return nil, err
	}
	for _, rec := range sets {
		var ss config.ScaleSet
		if err := json.Unmarshal([]byte(rec.Spec), &ss); err != nil {
			return nil, fmt.Errorf("settings: scale set %s: %w", rec.Name, err)
		}
		r.uiSets[rec.Name] = ss
	}
	raw, err := s.GetMeta(ctx, capacityKey)
	switch {
	case err == nil:
		// A row that does not decode or validate falls back to the defaults instead of
		// stopping the control plane (or scheduling nothing); Warnings reports it.
		var c config.Capacity
		err := json.Unmarshal([]byte(raw), &c)
		if err == nil {
			c.ApplyDefaults()
			err = c.Validate()
		}
		if err != nil {
			r.loadWarning = fmt.Sprintf("the capacity limits saved in the UI are ignored (%v); the defaults apply until they are saved again", err)
		} else {
			r.uiCap = &c
		}
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	return r, nil
}

// Capacity returns the capacity limits and where they come from: the file's capacity
// section wins, then the UI's, then the defaults.
func (r *Registry) Capacity() (config.Capacity, string) {
	if r.cfg.CapacityInFile {
		return r.cfg.Capacity, SourceFile
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.uiCap != nil {
		return *r.uiCap, SourceUI
	}
	return r.cfg.Capacity, CapacityDefault
}

// Warnings lists settings that cannot work as they are: ignored capacity limits, and
// scale sets whose environments are larger than the memory budget (their jobs never start).
func (r *Registry) Warnings() []string {
	var out []string
	if r.loadWarning != "" {
		out = append(out, r.loadWarning)
	}
	budget := r.CapacityLimits().MemoryBudgetMB
	for _, ss := range r.ScaleSets() {
		if budget > 0 && ss.MemoryMB > budget {
			out = append(out, fmt.Sprintf("scale set %s runs %d MB environments, above the %d MB memory budget; its jobs never start", ss.Name, ss.MemoryMB, budget))
		}
	}
	return out
}

// CapacityLimits returns the capacity limits in effect, for the scheduler.
func (r *Registry) CapacityLimits() config.Capacity {
	c, _ := r.Capacity()
	return c
}

// PutCapacity sets the capacity limits, unless the file has a capacity section. The
// memory budget must fit every scale set's environment, or its jobs would never start.
func (r *Registry) PutCapacity(ctx context.Context, c config.Capacity) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if r.cfg.CapacityInFile {
		return ErrReadOnly
	}
	if err := c.Validate(); err != nil {
		return err
	}
	for _, ss := range r.ScaleSets() {
		if ss.MemoryMB > c.MemoryBudgetMB {
			return fmt.Errorf("settings: the memory budget (%d MB) is smaller than an environment of scale set %s (%d MB); its jobs would never start",
				c.MemoryBudgetMB, ss.Name, ss.MemoryMB)
		}
	}
	spec, err := json.Marshal(c)
	if err != nil {
		return err
	}
	r.mu.Lock()
	err = r.store.PutMeta(ctx, capacityKey, string(spec))
	if err == nil {
		r.uiCap = &c
	}
	r.mu.Unlock()
	if err == nil {
		r.notify()
	}
	return err
}

func (r *Registry) fileCredential(name string) (config.Credential, bool) {
	return r.cfg.Credential(name)
}

func (r *Registry) fileScaleSet(name string) (config.ScaleSet, bool) {
	for _, ss := range r.cfg.ScaleSets {
		if ss.Name == name {
			return ss, true
		}
	}
	return config.ScaleSet{}, false
}

// Credentials lists file credentials, then UI ones, each sorted by name.
func (r *Registry) Credentials() []Credential {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Credential
	for _, c := range r.cfg.GitHub.Credentials {
		out = append(out, Credential{Name: c.Name, Source: SourceFile, Token: c.Token})
	}
	slices.SortFunc(out, func(a, b Credential) int { return strings.Compare(a.Name, b.Name) })
	ui := make([]Credential, 0, len(r.uiCreds))
	for _, c := range r.uiCreds {
		ui = append(ui, c)
	}
	slices.SortFunc(ui, func(a, b Credential) int { return strings.Compare(a.Name, b.Name) })
	return append(out, ui...)
}

// Credential returns a credential by name.
func (r *Registry) Credential(name string) (Credential, bool) {
	if c, ok := r.fileCredential(name); ok {
		return Credential{Name: c.Name, Source: SourceFile, Token: c.Token}, true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.uiCreds[name]
	return c, ok
}

// CredentialUsers returns the scale sets that use a credential.
func (r *Registry) CredentialUsers(name string) []string {
	var out []string
	for _, ss := range r.ScaleSets() {
		if ss.Credential == name {
			out = append(out, ss.Name)
		}
	}
	return out
}

// PutCredential creates or replaces a UI credential's token.
func (r *Registry) PutCredential(ctx context.Context, name, token string) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	token = strings.TrimSpace(token)
	if !config.ValidName(name) {
		return fmt.Errorf("settings: credential name %q must be lower-case letters, digits and '-'", name)
	}
	if token == "" {
		return errors.New("settings: the token is empty")
	}
	if _, ok := r.fileCredential(name); ok {
		return ErrReadOnly
	}
	sealed, err := r.vault.Seal(config.VaultGitHubPrefix+name, token)
	if err != nil {
		return err
	}
	r.mu.Lock()
	err = r.store.PutCredentialWithSecret(ctx, name, "github-pat", config.VaultGitHubPrefix+name, sealed)
	if err == nil {
		r.uiCreds[name] = Credential{Name: name, Source: SourceUI, Token: token}
	}
	r.mu.Unlock()
	if err == nil {
		r.notify()
	}
	return err
}

// DeleteCredential removes a UI credential that no scale set uses.
func (r *Registry) DeleteCredential(ctx context.Context, name string) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if _, ok := r.fileCredential(name); ok {
		return ErrReadOnly
	}
	if len(r.CredentialUsers(name)) > 0 {
		return ErrInUse
	}
	r.mu.Lock()
	if _, ok := r.uiCreds[name]; !ok {
		r.mu.Unlock()
		return store.ErrNotFound
	}
	err := r.store.DeleteCredentialWithSecret(ctx, name, config.VaultGitHubPrefix+name)
	if err == nil {
		delete(r.uiCreds, name)
	}
	r.mu.Unlock()
	if err == nil {
		r.notify()
	}
	return err
}

// ScaleSets lists file scale sets (in file order), then UI ones by name.
func (r *Registry) ScaleSets() []ScaleSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ScaleSet, 0, len(r.cfg.ScaleSets)+len(r.uiSets))
	for _, ss := range r.cfg.ScaleSets {
		out = append(out, ScaleSet{ScaleSet: ss, Source: SourceFile})
	}
	names := make([]string, 0, len(r.uiSets))
	for n := range r.uiSets {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		out = append(out, ScaleSet{ScaleSet: r.uiSets[n], Source: SourceUI})
	}
	return out
}

// ScaleSetConfigs returns the scale sets' settings, for the controller and listeners.
func (r *Registry) ScaleSetConfigs() []config.ScaleSet {
	all := r.ScaleSets()
	out := make([]config.ScaleSet, len(all))
	for i, ss := range all {
		out[i] = ss.ScaleSet
	}
	return out
}

// ScaleSet returns a scale set by name.
func (r *Registry) ScaleSet(name string) (ScaleSet, bool) {
	if ss, ok := r.fileScaleSet(name); ok {
		return ScaleSet{ScaleSet: ss, Source: SourceFile}, true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ss, ok := r.uiSets[name]
	return ScaleSet{ScaleSet: ss, Source: SourceUI}, ok
}

// PutScaleSet creates or replaces a UI scale set (defaults applied, then validated).
func (r *Registry) PutScaleSet(ctx context.Context, ss config.ScaleSet) error {
	return r.putScaleSet(ctx, ss, false)
}

// CreateScaleSet is PutScaleSet that never replaces a scale set: ErrExists if the name is taken.
func (r *Registry) CreateScaleSet(ctx context.Context, ss config.ScaleSet) error {
	return r.putScaleSet(ctx, ss, true)
}

func (r *Registry) putScaleSet(ctx context.Context, ss config.ScaleSet, create bool) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if create {
		if _, ok := r.ScaleSet(ss.Name); ok {
			return ErrExists
		}
	}
	if _, ok := r.fileScaleSet(ss.Name); ok {
		return ErrReadOnly
	}
	ss.ApplyDefaults()
	if err := ss.Validate(func(name string) bool { _, ok := r.Credential(name); return ok }); err != nil {
		return err
	}
	// A scale set already above the budget (the file's budget was lowered) stays editable
	// as long as its memory does not change.
	old, exists := r.ScaleSet(ss.Name)
	if budget := r.CapacityLimits().MemoryBudgetMB; budget > 0 && ss.MemoryMB > budget && (!exists || old.MemoryMB != ss.MemoryMB) {
		return fmt.Errorf("settings: an environment of %d MB does not fit the memory budget (%d MB); its jobs would never start", ss.MemoryMB, budget)
	}
	spec, err := json.Marshal(ss)
	if err != nil {
		return err
	}
	r.mu.Lock()
	err = r.store.PutScaleSetConfig(ctx, ss.Name, string(spec))
	if err == nil {
		r.uiSets[ss.Name] = ss
	}
	r.mu.Unlock()
	if err == nil {
		r.notify()
	}
	return err
}

// DeleteScaleSet removes a UI scale set.
func (r *Registry) DeleteScaleSet(ctx context.Context, name string) error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if _, ok := r.fileScaleSet(name); ok {
		return ErrReadOnly
	}
	r.mu.Lock()
	if _, ok := r.uiSets[name]; !ok {
		r.mu.Unlock()
		return store.ErrNotFound
	}
	err := r.store.DeleteScaleSetConfig(ctx, name)
	if err == nil {
		delete(r.uiSets, name)
	}
	r.mu.Unlock()
	if err == nil {
		r.notify()
	}
	return err
}

// Subscribe returns a channel that receives (coalesced) change notifications.
func (r *Registry) Subscribe() (<-chan struct{}, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextSub
	r.nextSub++
	ch := make(chan struct{}, 1)
	r.subs[id] = ch
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.subs, id)
	}
}

func (r *Registry) notify() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ch := range r.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
