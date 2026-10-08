package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"
)

// Profile says what a template preinstalls and what of GitHub's ubuntu-slim recipe it
// leaves out. Each profile has its own template versions; a scale set picks one.
type Profile struct {
	Name string `json:"name"`
	// Remove lists optional components of the recipe (Components) not to install.
	Remove []string `json:"remove,omitempty"`
	// Toolcache lists versions to preinstall in the hosted tool cache, per tool (node,
	// python, go), as version prefixes ("22", "3.12"): actions/setup-* then find them.
	Toolcache map[string][]string `json:"toolcache,omitempty"`
	// Apt lists extra Ubuntu packages.
	Apt []string `json:"apt,omitempty"`
	// Script runs as root at the end of the build (bash).
	Script string `json:"script,omitempty"`
	// SavedAt is when the profile was last saved (not part of the spec).
	SavedAt time.Time `json:"-"`
}

// JSON is the normalized profile, as stored with each template version built from it.
func (p Profile) JSON() []byte {
	b, _ := json.Marshal(p.Normalize())
	return b
}

// Component is an optional part of GitHub's ubuntu-slim recipe: one install script.
type Component struct {
	ID string `json:"id"`
	// Report lists the software report's tool names it installs, explained when it is left out.
	Report []string `json:"report"`
	// Needs names a component it depends on.
	Needs string `json:"needs,omitempty"`
}

// Components are the recipe's install scripts a profile may leave out. The rest (apt
// sources, git, the Docker CLI repository the ghrm layer installs from, PowerShell the
// software report runs on) are always installed.
var Components = []Component{
	{ID: "aws-tools", Report: []string{"AWS CLI", "AWS CLI Session Manager Plugin", "AWS SAM CLI"}},
	{ID: "azcopy", Report: []string{"AzCopy"}},
	{ID: "azure-cli", Report: []string{"Azure CLI"}},
	{ID: "azure-devops-cli", Report: []string{"Azure CLI (azure-devops)"}, Needs: "azure-cli"},
	{ID: "bicep", Report: []string{"Bicep"}},
	{ID: "git-lfs", Report: []string{"Git LFS"}},
	{ID: "github-cli", Report: []string{"GitHub CLI"}},
	{ID: "google-cloud-cli", Report: []string{"Google Cloud CLI"}},
	{ID: "nodejs", Report: []string{"Node.js", "Npm"}},
	{ID: "nvm", Report: []string{"nvm"}},
	// Python itself stays: the recipe's apt packages install python3 (python-is-python3).
	{ID: "python", Report: []string{"Pip", "Pip3", "Pipx"}},
	{ID: "yq", Report: []string{"yq"}},
	{ID: "zstd", Report: []string{"zstd"}},
}

// ToolcacheTools are the tools a profile can preinstall in the hosted tool cache.
var ToolcacheTools = []string{"go", "node", "python"}

// DefaultProfile is GitHub's recipe unchanged, plus Node.js 22 and 24 in the tool cache:
// the slim image ships no tool cache, so actions/setup-node would download them in every job.
func DefaultProfile() Profile {
	return Profile{Name: "default", Toolcache: map[string][]string{"node": {"22", "24"}}}
}

var (
	versionRe = regexp.MustCompile(`^\d+(\.\d+){0,2}$`)
	aptRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(:[a-z0-9]+)?(=[A-Za-z0-9.+~:-]+)?$`)
)

// Limits of a profile: the custom script, the extra packages, the versions per tool.
const (
	maxScript   = 64 << 10
	maxApt      = 100
	maxVersions = 10
)

// Normalize sorts and deduplicates the lists, so equal profiles hash equally.
func (p Profile) Normalize() Profile {
	norm := func(xs []string) []string {
		var out []string
		for _, x := range xs {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		sort.Strings(out)
		return slices.Compact(out)
	}
	p.Remove, p.Apt = norm(p.Remove), norm(p.Apt)
	tc := map[string][]string{}
	for tool, vs := range p.Toolcache {
		if vs = norm(vs); len(vs) > 0 {
			tc[tool] = vs
		}
	}
	p.Toolcache = tc
	if len(tc) == 0 {
		p.Toolcache = nil
	}
	p.Script = strings.TrimRight(strings.ReplaceAll(p.Script, "\r\n", "\n"), "\n ")
	return p
}

// Validate checks a normalized profile.
func (p Profile) Validate() error {
	var errs []error
	if !config.ValidName(p.Name) {
		errs = append(errs, fmt.Errorf("profile name %q must use lower-case letters, digits and dashes", p.Name))
	}
	for _, id := range p.Remove {
		if _, ok := component(id); !ok {
			errs = append(errs, fmt.Errorf("unknown component %q", id))
		}
	}
	for _, id := range p.Remove {
		for _, c := range Components {
			if c.Needs == id && !slices.Contains(p.Remove, c.ID) {
				errs = append(errs, fmt.Errorf("%s needs %s: leave it out too", c.ID, id))
			}
		}
	}
	if len(p.Apt) > maxApt {
		errs = append(errs, fmt.Errorf("at most %d apt packages", maxApt))
	}
	for tool, vs := range p.Toolcache {
		if len(vs) > maxVersions {
			errs = append(errs, fmt.Errorf("tool cache: at most %d %s versions", maxVersions, tool))
		}
		if !slices.Contains(ToolcacheTools, tool) {
			errs = append(errs, fmt.Errorf("tool cache: unknown tool %q (use %s)", tool, strings.Join(ToolcacheTools, ", ")))
		}
		for _, v := range vs {
			if !versionRe.MatchString(v) {
				errs = append(errs, fmt.Errorf("tool cache: %s version %q must look like 22 or 3.12", tool, v))
			}
		}
	}
	for _, a := range p.Apt {
		if !aptRe.MatchString(a) {
			errs = append(errs, fmt.Errorf("apt package %q is not a package name (name, name:arch or name=version)", a))
		}
	}
	if len(p.Script) > maxScript {
		errs = append(errs, fmt.Errorf("the script is longer than %d KiB", maxScript>>10))
	}
	return errors.Join(errs...)
}

// Hash identifies what the profile installs; a change triggers a rebuild of its templates.
func (p Profile) Hash() string {
	q := p.Normalize()
	q.Name = ""
	b, _ := json.Marshal(q)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func component(id string) (Component, bool) {
	for _, c := range Components {
		if c.ID == id {
			return c, true
		}
	}
	return Component{}, false
}

// removedToolNames are the software report's tool names the profile leaves out, sorted.
func (p Profile) removedToolNames() []string {
	var out []string
	for _, id := range p.Remove {
		if c, ok := component(id); ok {
			out = append(out, c.Report...)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

func (p Profile) removedTools() map[string]bool {
	out := map[string]bool{}
	for _, name := range p.removedToolNames() {
		out[name] = true
	}
	return out
}

// BuildFiles are the profile's files in the layer: profile.sh installs the extra apt
// packages and the tool cache versions (readable and writable by the runner, as
// actions/setup-* adds versions there), then runs script.sh, the profile's script (empty
// without one), as root with nothing on its standard input.
func (p Profile) BuildFiles() map[string]string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\n# Generated from the template profile " + shellQuote(p.Name) + ".\nset -euo pipefail\n")
	if len(p.Apt) > 0 {
		b.WriteString("apt-get update\napt-get install -y --no-install-recommends")
		for _, a := range p.Apt {
			b.WriteString(" " + shellQuote(a))
		}
		b.WriteString("\napt-get clean\n")
	}
	tools := make([]string, 0, len(p.Toolcache))
	for tool := range p.Toolcache {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		b.WriteString("/tmp/ghrm-profile/toolcache.sh " + shellQuote(tool))
		for _, v := range p.Toolcache[tool] {
			b.WriteString(" " + shellQuote(v))
		}
		b.WriteString("\n")
	}
	b.WriteString(`if [ -d "$AGENT_TOOLSDIRECTORY" ]; then chmod -R a+rwX "$AGENT_TOOLSDIRECTORY"; fi` + "\n")
	b.WriteString("bash -eo pipefail /tmp/ghrm-profile/script.sh </dev/null\n")
	return map[string]string{"profile.sh": b.String(), "script.sh": p.Script + "\n"}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
