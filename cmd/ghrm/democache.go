package main

import (
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/cachemon"
	"github.com/cocardoso/gh-runners-manager/internal/config"
)

// demoCache simulates a busy registry cache for the demo: hits grow with time.
type demoCache struct {
	start time.Time
	now   func() time.Time
}

func (d demoCache) Status() cachemon.Status {
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	minutes := now.Sub(d.start).Minutes()
	s := cachemon.Status{Enabled: true, Address: "10.50.0.3", Up: true, CheckedAt: now,
		DiskUsed: 23<<30 + int64(minutes*float64(8<<20)), DiskBudget: 100 << 30}
	for i, o := range config.CacheOrigins {
		weight := float64(4 - i)
		hits := 40*weight + minutes*3*weight
		misses := 6*weight + minutes*0.2*weight
		s.Origins = append(s.Origins, cachemon.OriginStatus{Origin: o, Up: true, BlobHits: hits, BlobMisses: misses,
			ServedBytes: hits * 45e6, PulledBytes: misses * 45e6})
	}
	return s
}
