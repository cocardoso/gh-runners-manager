// Package scheduler decides how many environments to create for each scale set
// under the global capacity limits (spec §9). It is pure: no I/O and no clock.
package scheduler

import (
	"sort"
	"time"
)

// Demand describes what one scale set wants right now.
type Demand struct {
	ScaleSet      string
	Desired       int       // assigned jobs plus warm pool
	Live          int       // environments that exist and are not destroyed
	MaxConcurrent int       // per-scale-set limit
	MemoryMB      int       // memory limit of one environment
	WaitingSince  time.Time // when the oldest unserved demand appeared; zero if unknown
}

// Capacity is a snapshot of global limits and current usage.
type Capacity struct {
	MaxEnvironments    int
	LiveEnvironments   int
	MemoryBudgetMB     int
	CommittedMemoryMB  int // sum of memory limits of live environments
	HostAvailableMB    int
	MemoryMarginMB     int
	ThinPoolPercent    float64
	MaxThinPoolPercent float64 // 0 disables the disk check
}

// Reason explains why a scale set has demand that is not being served.
type Reason string

const (
	ReasonScaleSetLimit Reason = "scale_set_limit"
	ReasonGlobalLimit   Reason = "global_limit"
	ReasonMemoryBudget  Reason = "memory_budget"
	ReasonHostMemory    Reason = "host_memory"
	ReasonDisk          Reason = "disk"
)

// Plan is the scheduler's decision.
type Plan struct {
	Create  map[string]int    // environments to create per scale set
	Waiting map[string]Reason // why a scale set still has unserved demand
}

// Decide returns how many environments to create per scale set. The scale set
// that has waited longest is served first, then environments are handed out
// one at a time in that order.
func Decide(demands []Demand, c Capacity) Plan {
	plan := Plan{Create: map[string]int{}, Waiting: map[string]Reason{}}

	ordered := append([]Demand(nil), demands...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i].WaitingSince, ordered[j].WaitingSince
		switch {
		case a.Equal(b):
			return ordered[i].ScaleSet < ordered[j].ScaleSet
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		default:
			return a.Before(b)
		}
	})

	remaining := make([]int, len(ordered))
	for i, d := range ordered {
		if n := min(d.Desired, d.MaxConcurrent) - d.Live; n > 0 {
			remaining[i] = n
		}
	}

	for progress := true; progress; {
		progress = false
		for i, d := range ordered {
			if remaining[i] == 0 {
				continue
			}
			if reason, ok := blocked(d, c); ok {
				plan.Waiting[d.ScaleSet] = reason
				remaining[i] = 0 // capacity only shrinks within one decision
				continue
			}
			plan.Create[d.ScaleSet]++
			remaining[i]--
			c.LiveEnvironments++
			c.CommittedMemoryMB += d.MemoryMB
			c.HostAvailableMB -= d.MemoryMB
			progress = true
		}
	}

	for _, d := range ordered {
		if _, waiting := plan.Waiting[d.ScaleSet]; !waiting && d.Desired > d.MaxConcurrent {
			plan.Waiting[d.ScaleSet] = ReasonScaleSetLimit
		}
	}
	return plan
}

func blocked(d Demand, c Capacity) (Reason, bool) {
	switch {
	case c.MaxThinPoolPercent > 0 && c.ThinPoolPercent >= c.MaxThinPoolPercent:
		return ReasonDisk, true
	case c.LiveEnvironments >= c.MaxEnvironments:
		return ReasonGlobalLimit, true
	case c.CommittedMemoryMB+d.MemoryMB > c.MemoryBudgetMB:
		return ReasonMemoryBudget, true
	case c.HostAvailableMB-d.MemoryMB < c.MemoryMarginMB:
		return ReasonHostMemory, true
	}
	return "", false
}
