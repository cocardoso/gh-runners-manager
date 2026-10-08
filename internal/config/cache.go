package config

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// CacheOrigins are the registries the cache mirrors, in a fixed order.
var CacheOrigins = []string{"docker.io", "ghcr.io", "mcr.microsoft.com", "quay.io"}

// Cache is the pull-through registry cache on the job network. An empty address
// means no cache: templates are built without mirror settings.
type Cache struct {
	Address      string         `yaml:"address"`
	Ports        map[string]int `yaml:"ports"`         // mirror port per origin (reachable by jobs)
	MetricsPorts map[string]int `yaml:"metrics_ports"` // Prometheus port per origin (control plane only)
	ExporterPort int            `yaml:"exporter_port"` // disk usage exporter (control plane only)
}

// Enabled reports whether a cache is configured.
func (c Cache) Enabled() bool { return c.Address != "" }

func (c *Cache) applyDefaults() {
	if !c.Enabled() {
		return
	}
	if c.Ports == nil {
		c.Ports = map[string]int{}
		for i, o := range CacheOrigins {
			c.Ports[o] = 5000 + i
		}
	}
	if c.MetricsPorts == nil {
		c.MetricsPorts = map[string]int{}
		for i, o := range CacheOrigins {
			c.MetricsPorts[o] = 5100 + i
		}
	}
	if c.ExporterPort == 0 {
		c.ExporterPort = 5199
	}
}

func (c Cache) validate() error {
	if !c.Enabled() {
		return nil
	}
	var errs []error
	if ip := net.ParseIP(c.Address); ip == nil || ip.To4() == nil {
		errs = append(errs, fmt.Errorf("cache.address must be an IPv4 address, got %q", c.Address))
	}
	known := map[string]bool{}
	for _, o := range CacheOrigins {
		known[o] = true
	}
	used := map[int]string{}
	port := func(what string, p int) {
		if p < 1 || p > 65535 {
			errs = append(errs, fmt.Errorf("cache: %s port %d is out of range", what, p))
			return
		}
		if prev, ok := used[p]; ok {
			errs = append(errs, fmt.Errorf("cache: %s and %s both use port %d", prev, what, p))
		}
		used[p] = what
	}
	for _, m := range []map[string]int{c.Ports, c.MetricsPorts} {
		for o := range m {
			if !known[o] {
				errs = append(errs, fmt.Errorf("cache: unknown origin %q (known: %s)", o, strings.Join(CacheOrigins, ", ")))
			}
		}
	}
	for _, o := range CacheOrigins {
		p, ok := c.Ports[o]
		if !ok {
			errs = append(errs, fmt.Errorf("cache: no port for %s", o))
		} else {
			port(o, p)
		}
		if mp, ok := c.MetricsPorts[o]; ok {
			port(o+" metrics", mp)
		} else {
			errs = append(errs, fmt.Errorf("cache: no metrics port for %s", o))
		}
	}
	port("exporter", c.ExporterPort)
	return errors.Join(errs...)
}

func (c Cache) addr(port int) string { return net.JoinHostPort(c.Address, strconv.Itoa(port)) }

// Mirrors is the canonical "origin=host:port,..." list (empty without a cache); the
// template layer receives it and it is part of the layer version.
func (c Cache) Mirrors() string {
	if !c.Enabled() {
		return ""
	}
	parts := make([]string, 0, len(c.Ports))
	for _, o := range CacheOrigins {
		parts = append(parts, o+"="+c.addr(c.Ports[o]))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// MirrorAddrs are the host:port addresses jobs pull from, in origin order.
func (c Cache) MirrorAddrs() []string {
	if !c.Enabled() {
		return nil
	}
	out := make([]string, 0, len(CacheOrigins))
	for _, o := range CacheOrigins {
		out = append(out, c.addr(c.Ports[o]))
	}
	return out
}

// PrivateAddrs are the cache's addresses that jobs must not reach (metrics, exporter).
func (c Cache) PrivateAddrs() []string {
	if !c.Enabled() {
		return nil
	}
	out := make([]string, 0, len(CacheOrigins)+1)
	for _, o := range CacheOrigins {
		out = append(out, c.addr(c.MetricsPorts[o]))
	}
	return append(out, c.addr(c.ExporterPort))
}
