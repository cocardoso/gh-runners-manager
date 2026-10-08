package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// Difference is one item that differs between GitHub's published software report and
// the report generated inside a new template (spec §8.4).
type Difference struct {
	Kind     string `json:"kind"` // "missing", "extra" or "version"
	Name     string `json:"name"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	// Explained differences do not hold a version back; Reason says why they are expected.
	Explained bool   `json:"explained"`
	Reason    string `json:"reason,omitempty"`
}

// FidelityReport is stored with each template version.
type FidelityReport struct {
	Checks      []ingest.Check `json:"checks"`
	Differences []Difference   `json:"differences"`
	// Unexpected counts differences the layer does not explain; -1 when the published
	// report could not be compared.
	Unexpected int    `json:"unexpected"`
	Note       string `json:"note,omitempty"`
}

// layerItems are report tools the ghrm layer installs or reinstalls on purpose (spec §8.2):
// a version change or an extra entry for them is explained; a missing one never is.
var layerItems = map[string]bool{
	"docker server": true, "docker engine": true, "containerd": true, "docker client": true, "docker-buildx": true,
	"docker compose v2": true, "systemd version": true, "systemd": true, "ghrm-agent": true, "github actions runner": true,
}

// explain says whether a difference is expected, and why: the ghrm layer installs the
// item, GitHub's image version carries a build suffix, or the version is newer (the
// recipe installs the latest releases, so a build made after GitHub's picks up newer
// ones). A missing item never is, nor an older version than GitHub's, nor a new major
// version of a language runtime, the change most likely to break a workflow.
func explain(kind, name, expected, actual string) (bool, string) {
	if kind == "missing" {
		return false, ""
	}
	tool := name
	if i := strings.LastIndex(name, " / "); i >= 0 {
		tool = name[i+3:]
	}
	switch {
	case layerItems[strings.ToLower(strings.TrimSpace(tool))]:
		return true, "installed by the ghrm layer"
	case kind != "version":
		return false, ""
	case name == "Image Version":
		if actual != "" && strings.HasPrefix(expected, actual+".") {
			return true, "GitHub's image version adds a build suffix"
		}
		return false, ""
	case expected == "" || actual == "" || prerelease.MatchString(actual):
		return false, ""
	case strings.Contains(expected, ",") || strings.Contains(actual, ","):
		if listDrift(expected, actual) {
			return true, newerReason
		}
		return false, ""
	case compareVersions(actual, expected) <= 0:
		return false, ""
	case strings.Contains(name, "Language and Runtime") && !sameMajor(expected, actual):
		return false, ""
	}
	return true, newerReason
}

const newerReason = "newer release: built after GitHub's image, the recipe installed the latest"

// prerelease matches release candidates and previews, which are not drift.
var prerelease = regexp.MustCompile(`(?i)(^|[^a-z])(rc|alpha|beta|preview)[.\-]?\d*`)

// listDrift says whether every version GitHub lists (cached tool versions, "3.10.12,
// 3.12.3") is still there, at the same minor version or a newer patch.
func listDrift(expected, actual string) bool {
	have := strings.Split(actual, ",")
	for _, e := range strings.Split(expected, ",") {
		e = strings.TrimSpace(e)
		found := false
		for _, a := range have {
			a = strings.TrimSpace(a)
			if sameMinor(e, a) && compareVersions(a, e) >= 0 && !prerelease.MatchString(a) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sameMinor compares the first two numbers of two versions.
func sameMinor(a, b string) bool {
	na, nb := numbers.FindAllString(a, 2), numbers.FindAllString(b, 2)
	return len(na) == 2 && len(nb) == 2 && na[0] == nb[0] && na[1] == nb[1]
}

var (
	leadingNumber = regexp.MustCompile(`^(?:\d+:)?\D*?(\d+)`)
	numbers       = regexp.MustCompile(`\d+`)
)

// sameMajor compares the first number of two versions (after a Debian epoch).
func sameMajor(a, b string) bool {
	ma, mb := leadingNumber.FindStringSubmatch(a), leadingNumber.FindStringSubmatch(b)
	return ma != nil && mb != nil && ma[1] == mb[1]
}

// compareVersions compares the numbers of two versions in order (Debian epoch, then
// the version's numbers, so 1:9.18.39-0ubuntu0.24.04.7 is newer than ...04.2).
func compareVersions(a, b string) int {
	na, nb := numbers.FindAllString(a, -1), numbers.FindAllString(b, -1)
	for i := 0; i < len(na) && i < len(nb); i++ {
		x, _ := strconv.Atoi(na[i])
		y, _ := strconv.Atoi(nb[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return len(na) - len(nb)
}

type reportNode struct {
	NodeType string                 `json:"NodeType"`
	Title    string                 `json:"Title"`
	ToolName string                 `json:"ToolName"`
	Version  string                 `json:"Version"`
	Versions oneOrMany[string]      `json:"Versions"`
	Headers  string                 `json:"Headers"`
	Rows     oneOrMany[string]      `json:"Rows"`
	Children oneOrMany[*reportNode] `json:"Children"`
	Content  json.RawMessage        `json:"Content"`
}

// oneOrMany decodes a JSON array, or the bare value PowerShell's ConvertTo-Json
// writes for a one-element array (GitHub's reports have both).
type oneOrMany[T any] []T

func (o *oneOrMany[T]) UnmarshalJSON(b []byte) error {
	if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '[' {
		var many []T
		err := json.Unmarshal(t, &many)
		*o = many
		return err
	}
	if string(bytes.TrimSpace(b)) == "null" {
		*o = nil
		return nil
	}
	var one T
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	*o = oneOrMany[T]{one}
	return nil
}

// flatten maps "Section / Subsection / Tool" to its version for every leaf of a report.
func flatten(raw []byte) (map[string]string, error) {
	var root reportNode
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("template: software report: %w", err)
	}
	out := map[string]string{}
	var walk func(n *reportNode, path []string, top bool)
	walk = func(n *reportNode, path []string, top bool) {
		if n == nil {
			return
		}
		key := func(name string) string {
			return strings.Join(append(append([]string{}, path...), strings.TrimSuffix(strings.TrimSpace(name), ":")), " / ")
		}
		switch n.NodeType {
		case "HeaderNode":
			next := path
			if !top {
				next = append(append([]string{}, path...), n.Title)
			}
			for _, c := range n.Children {
				walk(c, next, false)
			}
		case "ToolVersionNode":
			out[key(n.ToolName)] = n.Version
		case "ToolVersionsListNode":
			out[key(n.ToolName)] = strings.Join(n.Versions, ", ")
		case "TableNode":
			for _, row := range n.Rows {
				name, rest, _ := strings.Cut(row, "|")
				out[key(name)] = rest
			}
		}
	}
	walk(&root, nil, true)
	return out, nil
}

func difference(kind, name, expected, actual string) Difference {
	ok, reason := explain(kind, name, expected, actual)
	return Difference{Kind: kind, Name: name, Expected: expected, Actual: actual, Explained: ok, Reason: reason}
}

// CompareReports diffs GitHub's published report with the one generated in a template.
func CompareReports(published, actual []byte, checks []ingest.Check) (FidelityReport, error) {
	rep := FidelityReport{Checks: checks, Differences: []Difference{}}
	want, err := flatten(published)
	if err != nil {
		return rep, err
	}
	got, err := flatten(actual)
	if err != nil {
		return rep, err
	}
	for name, ev := range want {
		av, ok := got[name]
		switch {
		case !ok:
			rep.Differences = append(rep.Differences, difference("missing", name, ev, ""))
		case av != ev:
			rep.Differences = append(rep.Differences, difference("version", name, ev, av))
		}
	}
	for name, av := range got {
		if _, ok := want[name]; !ok {
			rep.Differences = append(rep.Differences, difference("extra", name, "", av))
		}
	}
	sort.Slice(rep.Differences, func(i, j int) bool { return rep.Differences[i].Name < rep.Differences[j].Name })
	for _, d := range rep.Differences {
		if !d.Explained {
			rep.Unexpected++
		}
	}
	return rep, nil
}

// ExplainProfile explains the differences a profile causes: the tools it leaves out of
// GitHub's recipe, and the apt packages and tool cache versions it adds.
func (r *FidelityReport) ExplainProfile(p Profile) {
	removed := p.removedTools()
	r.Unexpected = 0
	for i, d := range r.Differences {
		tool := d.Name
		if j := strings.LastIndex(d.Name, " / "); j >= 0 {
			tool = d.Name[j+3:]
		}
		switch {
		case d.Explained:
		case d.Kind == "missing" && removed[tool]:
			r.Differences[i].Explained, r.Differences[i].Reason = true, "left out by the template profile"
		case d.Kind == "extra" && (strings.HasPrefix(d.Name, "Cached Tools") || slices.Contains(p.Apt, tool)):
			r.Differences[i].Explained, r.Differences[i].Reason = true, "added by the template profile"
		}
		if !r.Differences[i].Explained {
			r.Unexpected++
		}
	}
}
