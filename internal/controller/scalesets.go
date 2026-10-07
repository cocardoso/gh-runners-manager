package controller

import "github.com/cocardoso/gh-runners-manager/internal/config"

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
