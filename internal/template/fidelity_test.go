package template

import (
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
)

const published = `{"NodeType":"HeaderNode","Title":"Ubuntu-Slim","Children":[
 {"NodeType":"ToolVersionNode","ToolName":"OS Version:","Version":"24.04.3 LTS"},
 {"NodeType":"ToolVersionNode","ToolName":"Image Version:","Version":"20261005.17.1"},
 {"NodeType":"HeaderNode","Title":"Installed Software","Children":[
   {"NodeType":"HeaderNode","Title":"Language and Runtime","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Node.js","Version":"24.13.0"},
     {"NodeType":"ToolVersionNode","ToolName":"Python","Version":"3.12.3"}]},
   {"NodeType":"HeaderNode","Title":"Tools","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Git","Version":"2.51.0"},
     {"NodeType":"ToolVersionsListNode","ToolName":"Cached Tools","Versions":["1.2","1.3"]},
     {"NodeType":"TableNode","Headers":"Name|Version","Rows":["curl|8.5.0","jq|1.7.1"]}]}]}]}`

const actual = `{"NodeType":"HeaderNode","Title":"Ubuntu-Slim","Children":[
 {"NodeType":"ToolVersionNode","ToolName":"OS Version:","Version":"24.04.3 LTS"},
 {"NodeType":"ToolVersionNode","ToolName":"Image Version:","Version":"20261005.17.1"},
 {"NodeType":"HeaderNode","Title":"Installed Software","Children":[
   {"NodeType":"HeaderNode","Title":"Language and Runtime","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Node.js","Version":"24.14.0"}]},
   {"NodeType":"HeaderNode","Title":"Tools","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Git","Version":"2.51.0"},
     {"NodeType":"ToolVersionNode","ToolName":"Docker Server","Version":"28.4.0"},
     {"NodeType":"ToolVersionsListNode","ToolName":"Cached Tools","Versions":["1.2","1.3"]},
     {"NodeType":"TableNode","Headers":"Name|Version","Rows":["curl|8.5.0","jq|1.7.2"]}]}]}]}`

func TestCompareReports(t *testing.T) {
	checks := []ingest.Check{{Name: "docker hello-world", OK: true}}
	rep, err := CompareReports([]byte(published), []byte(actual), checks)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Difference{}
	for _, d := range rep.Differences {
		got[d.Kind+" "+d.Name] = d
	}
	want := map[string]Difference{
		"version Installed Software / Language and Runtime / Node.js": {Expected: "24.13.0", Actual: "24.14.0", Explained: true},
		"missing Installed Software / Language and Runtime / Python":  {Expected: "3.12.3"},
		"extra Installed Software / Tools / Docker Server":            {Actual: "28.4.0", Explained: true},
		"version Installed Software / Tools / jq":                     {Expected: "1.7.1", Actual: "1.7.2", Explained: true},
	}
	if len(got) != len(want) {
		t.Fatalf("differences = %+v", rep.Differences)
	}
	for k, w := range want {
		d, ok := got[k]
		if !ok || d.Expected != w.Expected || d.Actual != w.Actual || d.Explained != w.Explained {
			t.Errorf("%s = %+v (present %v), want %+v", k, d, ok, w)
		}
	}
	// Only the missing Python is unexpected; newer minor versions are build-date drift.
	if rep.Unexpected != 1 || len(rep.Checks) != 1 {
		t.Fatalf("unexpected = %d", rep.Unexpected)
	}
}

func TestCompareIdenticalAndMalformed(t *testing.T) {
	rep, err := CompareReports([]byte(published), []byte(published), nil)
	if err != nil || len(rep.Differences) != 0 || rep.Unexpected != 0 {
		t.Fatalf("identical = %+v, %v", rep, err)
	}
	if _, err := CompareReports([]byte(published), []byte("{"), nil); err == nil {
		t.Fatal("malformed actual report must be an error")
	}
}

func TestExplainedOnlyForLayerItems(t *testing.T) {
	pub := `{"NodeType":"HeaderNode","Title":"R","Children":[
	 {"NodeType":"ToolVersionNode","ToolName":"GitHub Actions Runner","Version":"2.338.0"},
	 {"NodeType":"ToolVersionNode","ToolName":"Docker Client","Version":"28.4.0"},
	 {"NodeType":"ToolVersionNode","ToolName":"Docker Engine API helper","Version":"1"}]}`
	act := `{"NodeType":"HeaderNode","Title":"R","Children":[
	 {"NodeType":"ToolVersionNode","ToolName":"Docker Client","Version":"28.5.1"},
	 {"NodeType":"ToolVersionNode","ToolName":"Docker Engine API helper","Version":"2"}]}`
	rep, err := CompareReports([]byte(pub), []byte(act), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	reasons := map[string]string{}
	for _, d := range rep.Differences {
		got[d.Kind+" "+d.Name] = d.Explained
		reasons[d.Kind+" "+d.Name] = d.Reason
	}
	if got["missing GitHub Actions Runner"] {
		t.Error("a missing runner is never explained by the layer")
	}
	if !got["version Docker Client"] {
		t.Error("the layer reinstalls the Docker CLI from Docker's repository: a version change is explained")
	}
	if reasons["version Docker Engine API helper"] == "installed by the ghrm layer" {
		t.Error("only exact layer items are explained by the layer, not substrings")
	}
}

// PowerShell's ConvertTo-Json writes a one-element array as a bare value, as in
// GitHub's published ubuntu-slim report ("PowerShell Tools", "Installed apt packages").
func TestCompareReportsWithSingleElementArrays(t *testing.T) {
	report := []byte(`{"NodeType":"HeaderNode","Title":"Ubuntu","Children":[
	  {"NodeType":"HeaderNode","Title":"Installed Software","Children":[
	    {"NodeType":"HeaderNode","Title":"PowerShell Tools","Children":{"NodeType":"ToolVersionNode","ToolName":"PowerShell","Version":"7.5.4"}},
	    {"NodeType":"HeaderNode","Title":"Installed apt packages","Children":{"NodeType":"TableNode","Headers":"Name|Version","Rows":"acl|2.3.2"}},
	    {"NodeType":"ToolVersionsListNode","ToolName":"Node.js","Versions":"22.20.0"}]}]}`)
	got, err := flatten(report)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Installed Software / PowerShell Tools / PowerShell": "7.5.4",
		"Installed Software / Installed apt packages / acl":  "2.3.2",
		"Installed Software / Node.js":                       "22.20.0",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	rep, err := CompareReports(report, report, nil)
	if err != nil || rep.Unexpected != 0 || len(rep.Differences) != 0 {
		t.Fatalf("identical single-element reports = %+v, %v", rep, err)
	}
}

// A build made days after GitHub's picks up newer releases (the recipe installs the
// latest), so newer versions are shown but do not block activation; a tool whose first
// number counts releases (Google Cloud CLI) is no exception. A runtime's new major
// version and an older version than GitHub's are not explained.
func TestVersionDriftIsExplainedButRuntimeMajorsAndDowngradesAreNot(t *testing.T) {
	published := []byte(`{"NodeType":"HeaderNode","Title":"Ubuntu-Slim","Children":[
	  {"NodeType":"ToolVersionNode","ToolName":"Image Version:","Version":"20260925.9.1"},
	  {"NodeType":"HeaderNode","Title":"Installed Software","Children":[
	    {"NodeType":"HeaderNode","Title":"Language and Runtime","Children":[
	      {"NodeType":"ToolVersionNode","ToolName":"Node.js","Version":"24.21.0"},
	      {"NodeType":"ToolVersionNode","ToolName":"Python","Version":"3.12.3"}]},
	    {"NodeType":"HeaderNode","Title":"CLI Tools","Children":[
	      {"NodeType":"ToolVersionNode","ToolName":"GitHub CLI","Version":"2.101.0"},
	      {"NodeType":"ToolVersionNode","ToolName":"Google Cloud CLI","Version":"586.0.0"},
	      {"NodeType":"ToolVersionNode","ToolName":"AWS CLI","Version":"2.37.4"}]},
	    {"NodeType":"TableNode","Headers":"Name|Version","Rows":["sudo|1.9.15p5-3ubuntu5.24.04.3","bind|1:9.18.39-0ubuntu0.24.04.2"]}]}]}`)
	actual := []byte(`{"NodeType":"HeaderNode","Title":"Ubuntu-Slim","Children":[
	  {"NodeType":"ToolVersionNode","ToolName":"Image Version:","Version":"20260925.9"},
	  {"NodeType":"HeaderNode","Title":"Installed Software","Children":[
	    {"NodeType":"HeaderNode","Title":"Language and Runtime","Children":[
	      {"NodeType":"ToolVersionNode","ToolName":"Node.js","Version":"26.1.0"},
	      {"NodeType":"ToolVersionNode","ToolName":"Python","Version":"3.12.8"}]},
	    {"NodeType":"HeaderNode","Title":"CLI Tools","Children":[
	      {"NodeType":"ToolVersionNode","ToolName":"GitHub CLI","Version":"2.102.0"},
	      {"NodeType":"ToolVersionNode","ToolName":"Google Cloud CLI","Version":"588.0.0"},
	      {"NodeType":"ToolVersionNode","ToolName":"AWS CLI","Version":"2.37.1"}]},
	    {"NodeType":"TableNode","Headers":"Name|Version","Rows":["sudo|1.9.15p5-3ubuntu5.24.04.4","bind|1:9.18.39-0ubuntu0.24.04.7"]}]}]}`)
	rep, err := CompareReports(published, actual, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Difference{}
	for _, d := range rep.Differences {
		got[d.Name] = d
	}
	for _, name := range []string{"Image Version", "Installed Software / Language and Runtime / Python", "Installed Software / CLI Tools / GitHub CLI",
		"Installed Software / CLI Tools / Google Cloud CLI", "Installed Software / sudo", "Installed Software / bind"} {
		if d := got[name]; !d.Explained || d.Reason == "" {
			t.Errorf("%s = %+v, want explained with a reason", name, d)
		}
	}
	for _, name := range []string{"Installed Software / Language and Runtime / Node.js", "Installed Software / CLI Tools / AWS CLI"} {
		if d := got[name]; d.Explained {
			t.Errorf("%s must not be explained: %+v", name, d)
		}
	}
	if rep.Unexpected != 2 {
		t.Fatalf("unexpected = %d, want 2 (the Node.js major change and the older AWS CLI)", rep.Unexpected)
	}
}

func TestExplainEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name, item, expected, actual string
		want                         bool
	}{
		{"a runtime version that disappeared from a list", "Installed Software / Language and Runtime / Python", "3.10.12, 3.12.3", "3.12.3", false},
		{"every runtime in a list moved within its minor", "Installed Software / Language and Runtime / Python", "3.10.12, 3.12.3", "3.10.14, 3.12.8", true},
		{"a list that gained a version", "Installed Software / Cached Tools / Go", "1.22.9", "1.22.9, 1.23.4", true},
		{"an empty image version", "Image Version", "20261005.17.1", "", false},
		{"an image version that is a shorter build", "Image Version", "20261005.17", "20261005.1", false},
		{"the image version's build suffix", "Image Version", "20261005.17.1", "20261005.17", true},
		{"a pre-release", "Installed Software / Tools / Bicep", "2.0.0", "2.0.0-rc1", false},
		{"nothing to compare with", "Installed Software / Tools / Bicep", "", "2.0.0", false},
		{"letters only differ (safe: not explained)", "Installed Software / Tools / OpenSSL", "1.1.1f", "1.1.1g", false},
	} {
		if got, _ := explain("version", tc.item, tc.expected, tc.actual); got != tc.want {
			t.Errorf("%s: explained = %v, want %v", tc.name, got, tc.want)
		}
	}
}
