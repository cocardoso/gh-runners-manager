package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	Log         io.Writer         // one line per eviction; nil discards
	// Run executes a command (systemctl, registry); exec when nil.
	Run func(ctx context.Context, name string, args ...string) error

	fsys fs.FS // the cache directory as read for measuring; os.DirFS(Root) when nil
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

func (p *CachePrune) fs() fs.FS {
	if p.fsys != nil {
		return p.fsys
	}
	return os.DirFS(p.Root)
}

// usage sums the sizes of the cache's files.
func (p *CachePrune) usage() (int64, error) {
	var total int64
	err := fs.WalkDir(p.fs(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// The registry removes uploads and expired content while it serves.
			if path != "." && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
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
		base := origin + "/docker/registry/v2/repositories"
		err := fs.WalkDir(p.fs(), base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				switch {
				case !errors.Is(err, fs.ErrNotExist):
					return err
				case path == base: // nothing cached for this origin yet
					return fs.SkipAll
				default: // removed while walking
					return nil
				}
			}
			if !d.IsDir() {
				return nil
			}
			if _, err := fs.Stat(p.fs(), path+"/_manifests"); err != nil {
				return nil
			}
			r := cachedRepo{origin: origin, dir: filepath.Join(p.Root, filepath.FromSlash(path))}
			_ = fs.WalkDir(p.fs(), path, func(_ string, f fs.DirEntry, err error) error {
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
			unit := "ghrm-cache-registry@" + r.origin
			// The registry must not serve while the repository and its garbage go.
			if err := p.run(ctx, "systemctl", "stop", unit); err != nil {
				return err
			}
			// Without --delete-untagged: that would also drop every image pulled only by
			// digest, in every repository. The deleted repository's blobs are unreferenced.
			evictErr := os.RemoveAll(r.dir)
			if evictErr == nil {
				evictErr = p.run(ctx, p.Registry, "garbage-collect", p.Instances[r.origin])
			}
			if err := p.run(ctx, "systemctl", "start", unit); err != nil {
				return err
			}
			if evictErr != nil {
				// What was deleted is gone; the exporter still gets the current usage.
				if used, err := p.usage(); err == nil {
					_ = p.writeStatus(used)
				}
				return evictErr
			}
			if used, err = p.usage(); err != nil {
				return err
			}
			if p.Log != nil {
				name, _ := filepath.Rel(filepath.Join(p.Root, r.origin, "docker/registry/v2/repositories"), r.dir)
				fmt.Fprintf(p.Log, "evicted %s/%s (last used %s); cache now %d of %d bytes\n",
					r.origin, name, r.lastUsed.UTC().Format(time.RFC3339), used, p.BudgetBytes)
			}
		}
	}
	return p.writeStatus(used)
}
