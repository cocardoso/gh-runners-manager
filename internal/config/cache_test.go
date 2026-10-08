package config

import (
	"strings"
	"testing"
)

func TestCacheDisabledByDefault(t *testing.T) {
	cfg, err := loadServe(t, serveYAML)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cache.Enabled() || cfg.Cache.Mirrors() != "" || len(cfg.Cache.MirrorAddrs()) != 0 {
		t.Fatalf("cache = %+v; want none", cfg.Cache)
	}
}

func TestCacheDefaultsAndMirrors(t *testing.T) {
	cfg, err := loadServe(t, serveYAML+"cache:\n  address: 10.50.0.3\n")
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Cache
	want := "docker.io=10.50.0.3:5000,ghcr.io=10.50.0.3:5001,mcr.microsoft.com=10.50.0.3:5002,quay.io=10.50.0.3:5003"
	if !c.Enabled() || c.Mirrors() != want {
		t.Fatalf("mirrors = %q, want %q", c.Mirrors(), want)
	}
	if got := strings.Join(c.MirrorAddrs(), ","); got != "10.50.0.3:5000,10.50.0.3:5001,10.50.0.3:5002,10.50.0.3:5003" {
		t.Fatalf("mirror addrs = %s", got)
	}
	if got := strings.Join(c.PrivateAddrs(), ","); got != "10.50.0.3:5100,10.50.0.3:5101,10.50.0.3:5102,10.50.0.3:5103,10.50.0.3:5199" {
		t.Fatalf("private addrs = %s", got)
	}
}

func TestCacheValidation(t *testing.T) {
	for name, body := range map[string]string{
		"not an address":    "cache:\n  address: cache.example\n",
		"unknown origin":    "cache:\n  address: 10.50.0.3\n  ports: {docker.io: 5000, ghcr.io: 5001, mcr.microsoft.com: 5002, quay.io: 5003, example.com: 5004}\n",
		"duplicate ports":   "cache:\n  address: 10.50.0.3\n  ports: {docker.io: 5000, ghcr.io: 5000, mcr.microsoft.com: 5002, quay.io: 5003}\n",
		"port out of range": "cache:\n  address: 10.50.0.3\n  exporter_port: 70000\n",
	} {
		if _, err := loadServe(t, serveYAML+body); err == nil || !strings.Contains(err.Error(), "cache") {
			t.Errorf("%s: err = %v, want a cache validation error", name, err)
		}
	}
}
