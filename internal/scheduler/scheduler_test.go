package scheduler

import (
	"testing"
	"time"
)

func roomy() Capacity {
	return Capacity{
		MaxEnvironments: 10, MemoryBudgetMB: 64 * 1024, HostAvailableMB: 64 * 1024,
		MemoryMarginMB: 4096, MaxThinPoolPercent: 85,
	}
}

func demand(name string, desired, live int) Demand {
	return Demand{ScaleSet: name, Desired: desired, Live: live, MaxConcurrent: 4, MemoryMB: 4096}
}

func TestCreatesUpToDesired(t *testing.T) {
	p := Decide([]Demand{demand("a", 2, 0)}, roomy())
	if p.Create["a"] != 2 || len(p.Waiting) != 0 {
		t.Fatalf("plan = %+v, want create 2 and nothing waiting", p)
	}
}

func TestAccountsForLiveEnvironments(t *testing.T) {
	p := Decide([]Demand{demand("a", 3, 2)}, roomy())
	if p.Create["a"] != 1 {
		t.Fatalf("create = %d, want 1", p.Create["a"])
	}
	p = Decide([]Demand{demand("a", 1, 3)}, roomy())
	if p.Create["a"] != 0 || len(p.Waiting) != 0 {
		t.Fatalf("plan = %+v, want nothing when live exceeds desired", p)
	}
}

func TestNothingToDo(t *testing.T) {
	p := Decide([]Demand{demand("a", 0, 0)}, roomy())
	if len(p.Create) != 0 || len(p.Waiting) != 0 {
		t.Fatalf("plan = %+v, want empty", p)
	}
}

func TestScaleSetLimit(t *testing.T) {
	d := demand("a", 5, 0)
	d.MaxConcurrent = 2
	p := Decide([]Demand{d}, roomy())
	if p.Create["a"] != 2 || p.Waiting["a"] != ReasonScaleSetLimit {
		t.Fatalf("plan = %+v, want create 2 and waiting scale_set_limit", p)
	}
}

func TestGlobalLimit(t *testing.T) {
	c := roomy()
	c.MaxEnvironments, c.LiveEnvironments = 3, 2
	p := Decide([]Demand{demand("a", 2, 0)}, c)
	if p.Create["a"] != 1 || p.Waiting["a"] != ReasonGlobalLimit {
		t.Fatalf("plan = %+v, want create 1 and waiting global_limit", p)
	}
}

func TestMemoryBudget(t *testing.T) {
	c := roomy()
	c.MemoryBudgetMB, c.CommittedMemoryMB = 8192, 4096
	d := demand("a", 1, 0)
	d.MemoryMB = 6144
	p := Decide([]Demand{d}, c)
	if p.Create["a"] != 0 || p.Waiting["a"] != ReasonMemoryBudget {
		t.Fatalf("plan = %+v, want waiting memory_budget", p)
	}
}

func TestHostMemoryMargin(t *testing.T) {
	c := roomy()
	c.HostAvailableMB = 9000
	d := demand("a", 1, 0)
	d.MemoryMB = 6144
	p := Decide([]Demand{d}, c)
	if p.Create["a"] != 0 || p.Waiting["a"] != ReasonHostMemory {
		t.Fatalf("plan = %+v, want waiting host_memory", p)
	}
}

func TestDisk(t *testing.T) {
	c := roomy()
	c.ThinPoolPercent = 90
	p := Decide([]Demand{demand("a", 1, 0)}, c)
	if p.Create["a"] != 0 || p.Waiting["a"] != ReasonDisk {
		t.Fatalf("plan = %+v, want waiting disk", p)
	}
	c.MaxThinPoolPercent = 0 // disabled
	if p := Decide([]Demand{demand("a", 1, 0)}, c); p.Create["a"] != 1 {
		t.Fatalf("disk check must be disabled when MaxThinPoolPercent is 0, plan = %+v", p)
	}
}

func TestOldestFirstThenRoundRobin(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	older, newer := demand("newer-name-a", 2, 0), demand("z-older", 2, 0)
	older.WaitingSince, newer.WaitingSince = t0.Add(time.Minute), t0
	c := roomy()
	c.MaxEnvironments = 1
	p := Decide([]Demand{older, newer}, c)
	if p.Create["z-older"] != 1 || p.Create["newer-name-a"] != 0 {
		t.Fatalf("plan = %+v, want the oldest waiting scale set served first", p)
	}
	c.MaxEnvironments = 2
	p = Decide([]Demand{older, newer}, c)
	if p.Create["z-older"] != 1 || p.Create["newer-name-a"] != 1 {
		t.Fatalf("plan = %+v, want one each (round-robin)", p)
	}
}

func TestDemandWithoutWaitingSinceGoesLast(t *testing.T) {
	waiting := demand("b", 1, 0)
	waiting.WaitingSince = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := roomy()
	c.MaxEnvironments = 1
	p := Decide([]Demand{demand("a", 1, 0), waiting}, c)
	if p.Create["b"] != 1 {
		t.Fatalf("plan = %+v, want the scale set with a waiting time first", p)
	}
}
