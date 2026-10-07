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
		"version Installed Software / Language and Runtime / Node.js": {Expected: "24.13.0", Actual: "24.14.0"},
		"missing Installed Software / Language and Runtime / Python":  {Expected: "3.12.3"},
		"extra Installed Software / Tools / Docker Server":            {Actual: "28.4.0", Explained: true},
		"version Installed Software / Tools / jq":                     {Expected: "1.7.1", Actual: "1.7.2"},
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
	if rep.Unexpected != 3 || len(rep.Checks) != 1 {
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
	for _, d := range rep.Differences {
		got[d.Kind+" "+d.Name] = d.Explained
	}
	if got["missing GitHub Actions Runner"] {
		t.Error("a missing runner is never explained by the layer")
	}
	if !got["version Docker Client"] {
		t.Error("the layer reinstalls the Docker CLI from Docker's repository: a version change is explained")
	}
	if got["version Docker Engine API helper"] {
		t.Error("only exact layer items are explained, not substrings")
	}
}
