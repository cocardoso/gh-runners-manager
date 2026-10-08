package agent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CachePrune keeps the registry cache under its disk budget (M6). The registry's proxy
// expires content by age only; when the cache is above HighPercent of the budget,
// CachePrune deletes the least recently used repositories, one at a time, and runs the
// registry's garbage collection for that instance, until usage is below LowPercent.
type CachePrune struct {
	Root        string            // one directory per origin: <root>/<origin>/docker/registry/v2/...
	BudgetBytes int64             // the cache's disk budget
	HighPercent int               // prune above this share of the budget
	LowPercent  int               // until below this share
	Instances   map[string]string // origin -> registry configuration file
	StatusPath  string            // where used and budget bytes are written for the exporter
	Registry    string            // the registry binary, by absolute path (pct exec has a short PATH)
	// Run executes a command (systemctl, registry); exec when nil.
	Run func(ctx context.Context, name string, args ...string) error
}

type cachedRepo struct {
	origin, dir string
	lastUsed    time.Time
}

func (p *CachePrune) run(ctx context.Context, name string, args ...string) error {
	if p.Run != nil {
		return p.Run(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// usage sums the sizes of the cache's files.
func (p *CachePrune) usage() (int64, error) {
	var total int64
	err := filepath.WalkDir(p.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// repos lists the cached repositories with the last time any of their files was read
// or written (tag links are read on every pull of a tag).
func (p *CachePrune) repos() ([]cachedRepo, error) {
	var out []cachedRepo
	for origin := range p.Instances {
		base := filepath.Join(p.Root, origin, "docker/registry/v2/repositories")
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return filepath.SkipAll
				}
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if _, err := os.Stat(filepath.Join(path, "_manifests")); err != nil {
				return nil
			}
			r := cachedRepo{origin: origin, dir: path}
			_ = filepath.WalkDir(path, func(_ string, f fs.DirEntry, err error) error {
				if err == nil && f.Type().IsRegular() {
					if info, err := f.Info(); err == nil {
						if t := lastUse(info); t.After(r.lastUsed) {
							r.lastUsed = t
						}
					}
				}
				return nil
			})
			out = append(out, r)
			return filepath.SkipDir
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lastUsed.Before(out[j].lastUsed) })
	return out, nil
}

func (p *CachePrune) writeStatus(used int64) error {
	if p.StatusPath == "" {
		return nil
	}
	tmp := p.StatusPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(fmt.Sprintf("used_bytes %d\nbudget_bytes %d\n", used, p.BudgetBytes)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.StatusPath)
}

// Once prunes when needed and records the usage.
func (p *CachePrune) Once(ctx context.Context) error {
	used, err := p.usage()
	if err != nil {
		return err
	}
	high, low := p.BudgetBytes*int64(p.HighPercent)/100, p.BudgetBytes*int64(p.LowPercent)/100
	if used > high {
		repos, err := p.repos()
		if err != nil {
			return err
		}
		for _, r := range repos {
			if used < low {
				break
			}
			if err := os.RemoveAll(r.dir); err != nil {
				return err
			}
			unit := "ghrm-cache-registry@" + r.origin
			// The registry must not serve while its garbage is collected.
			if err := p.run(ctx, "systemctl", "stop", unit); err != nil {
				return err
			}
			gcErr := p.run(ctx, p.Registry, "garbage-collect", "--delete-untagged", p.Instances[r.origin])
			if err := p.run(ctx, "systemctl", "start", unit); err != nil {
				return err
			}
			if gcErr != nil {
				// What was deleted is gone; the exporter still gets the current usage.
				if used, err := p.usage(); err == nil {
					_ = p.writeStatus(used)
				}
				return gcErr
			}
			if used, err = p.usage(); err != nil {
				return err
			}
		}
	}
	return p.writeStatus(used)
}
