package template

import (
	"strings"
	"testing"
)

func TestProfileValidation(t *testing.T) {
	ok := []Profile{
		DefaultProfile(),
		{Name: "lean", Remove: []string{"azure-cli", "azure-devops-cli", "google-cloud-cli"}, Toolcache: map[string][]string{"python": {"3.12"}, "go": {"1.24.2"}}, Apt: []string{"libpq-dev", "g++"}},
	}
	for _, p := range ok {
		if err := p.Normalize().Validate(); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}
	}
	for want, p := range map[string]Profile{
		"name":               {Name: "Not Valid"},
		"unknown component":  {Name: "x", Remove: []string{"docker-cli"}},
		"needs azure-cli":    {Name: "x", Remove: []string{"azure-cli"}},
		"unknown tool":       {Name: "x", Toolcache: map[string][]string{"ruby": {"3"}}},
		"must look like":     {Name: "x", Toolcache: map[string][]string{"node": {"lts"}}},
		"not a package name": {Name: "x", Apt: []string{"libfoo; rm -rf /"}},
		"longer than":        {Name: "x", Script: strings.Repeat("a", maxScript+1)},
	} {
		err := p.Normalize().Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestProfileHashIgnoresOrderAndName(t *testing.T) {
	a := Profile{Name: "a", Remove: []string{"yq", "bicep"}, Apt: []string{"jq", " zip "}, Toolcache: map[string][]string{"node": {"24", "22"}, "go": {}}}
	b := Profile{Name: "b", Remove: []string{"bicep", "yq", "yq"}, Apt: []string{"zip", "jq"}, Toolcache: map[string][]string{"node": {"22", "24"}}}
	if a.Hash() != b.Hash() {
		t.Fatal("equal profiles must hash equally")
	}
	b.Script = "echo hi"
	if a.Hash() == b.Hash() {
		t.Fatal("a script changes the hash")
	}
}
