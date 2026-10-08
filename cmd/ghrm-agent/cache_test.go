package main

import (
	"testing"
)

func TestCachePruneFlags(t *testing.T) {
	p, err := cachePruneFromArgs([]string{"--root", "/var/lib/ghrm-cache", "--budget-gb", "100",
		"--instance", "docker.io=/etc/ghrm-cache/docker.io.yml", "--instance", "quay.io=/etc/ghrm-cache/quay.io.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != "/var/lib/ghrm-cache" || p.BudgetBytes != 100<<30 || p.HighPercent != 85 || p.LowPercent != 70 ||
		len(p.Instances) != 2 || p.Instances["quay.io"] != "/etc/ghrm-cache/quay.io.yml" || p.StatusPath != "/var/lib/ghrm-cache/status" {
		t.Fatalf("prune = %+v", p)
	}
	if _, err := cachePruneFromArgs([]string{"--instance", "bad"}); err == nil {
		t.Fatal("an instance without a configuration must be refused")
	}
	if _, err := cachePruneFromArgs([]string{"--root", "/x", "--budget-gb", "0"}); err == nil {
		t.Fatal("a zero budget must be refused")
	}
}
