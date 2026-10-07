package controller

import (
	"context"

	"github.com/cocardoso/gh-runners-manager/internal/config"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// UpdateScaleSets applies the current scale set settings: new ones are added, changed
// ones use their new settings for new environments, and removed ones drain (their live
// environments finish; they get no new ones and disappear once empty).
func (c *Controller) UpdateScaleSets(list []config.ScaleSet) {
	c.mu.Lock()
	defer c.mu.Unlock()
	present := map[string]bool{}
	order := make([]string, 0, len(list)+len(c.order))
	for _, ss := range list {
		present[ss.Name] = true
		order = append(order, ss.Name)
		if s, ok := c.scaleSets[ss.Name]; ok {
			s.cfg, s.removed = ss, false
		} else {
			c.scaleSets[ss.Name] = &scaleSetState{cfg: ss}
		}
	}
	for _, name := range c.order {
		if !present[name] {
			c.scaleSets[name].removed = true
			order = append(order, name)
		}
	}
	c.order = order
	c.Kick()
}

// Draining returns the settings of removed scale sets that still have environments, so
// their listeners keep running (job messages, runner removal) until they are empty.
// Removed scale sets without environments are forgotten.
func (c *Controller) Draining(ctx context.Context) []config.ScaleSet {
	envs, err := c.d.Store.ListEnvironments(ctx, store.EnvironmentFilter{States: liveStates})
	if err != nil {
		return nil
	}
	live := map[string]int{}
	for _, e := range envs {
		live[e.ScaleSet]++
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []config.ScaleSet
	keep := c.order[:0]
	for _, name := range c.order {
		s := c.scaleSets[name]
		if s.removed && live[name] == 0 {
			delete(c.scaleSets, name)
			continue
		}
		keep = append(keep, name)
		if s.removed {
			out = append(out, s.cfg)
		}
	}
	c.order = keep
	return out
}
