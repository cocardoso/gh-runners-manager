package template

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

// Difference is one item that differs between GitHub's published software report and
// the report generated inside a new template (spec §8.4).
type Difference struct {
	Kind      string `json:"kind"` // "missing", "extra" or "version"
	Name      string `json:"name"`
	Expected  string `json:"expected,omitempty"`
	Actual    string `json:"actual,omitempty"`
	Explained bool   `json:"explained"` // an item the ghrm layer adds on purpose
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

func explained(kind, name string) bool {
	if kind == "missing" {
		return false
	}
	tool := name
	if i := strings.LastIndex(name, " / "); i >= 0 {
		tool = name[i+3:]
	}
	return layerItems[strings.ToLower(strings.TrimSpace(tool))]
}

type reportNode struct {
	NodeType string          `json:"NodeType"`
	Title    string          `json:"Title"`
	ToolName string          `json:"ToolName"`
	Version  string          `json:"Version"`
	Versions []string        `json:"Versions"`
	Headers  string          `json:"Headers"`
	Rows     []string        `json:"Rows"`
	Children []*reportNode   `json:"Children"`
	Content  json.RawMessage `json:"Content"`
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
			rep.Differences = append(rep.Differences, Difference{Kind: "missing", Name: name, Expected: ev, Explained: explained("missing", name)})
		case av != ev:
			rep.Differences = append(rep.Differences, Difference{Kind: "version", Name: name, Expected: ev, Actual: av, Explained: explained("version", name)})
		}
	}
	for name, av := range got {
		if _, ok := want[name]; !ok {
			rep.Differences = append(rep.Differences, Difference{Kind: "extra", Name: name, Actual: av, Explained: explained("extra", name)})
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
