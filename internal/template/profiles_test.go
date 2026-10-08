package template

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/config"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

var lean = Profile{Name: "lean", Remove: []string{"nodejs"}, Apt: []string{"zip"}, Toolcache: map[string][]string{"go": {"1.24"}}, Script: "echo 'hi'"}

// The software report of a template built from lean: no Node.js nor Npm (the report lists
// only the recipe's own apt packages, so zip shows nowhere).
const leanReport = `{"NodeType":"HeaderNode","Title":"Ubuntu-Slim","Children":[
 {"NodeType":"ToolVersionNode","ToolName":"OS Version:","Version":"24.04.3 LTS"},
 {"NodeType":"ToolVersionNode","ToolName":"Image Version:","Version":"20261005.17.1"},
 {"NodeType":"HeaderNode","Title":"Installed Software","Children":[
   {"NodeType":"HeaderNode","Title":"Language and Runtime","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Python","Version":"3.12.3"}]},
   {"NodeType":"HeaderNode","Title":"Tools","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Git","Version":"2.51.0"},
     {"NodeType":"ToolVersionsListNode","ToolName":"Cached Tools","Versions":["1.2","1.3"]},
     {"NodeType":"TableNode","Headers":"Name|Version","Rows":["curl|8.5.0","jq|1.7.1"]}]}]}]}`

func TestAProfileBuildsItsOwnTemplate(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	if err := h.s.PutProfile(ctx, lean); err != nil {
		t.Fatal(err)
	}
	tpl, err := h.s.Build(ctx, "manual", "lean")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Profile != "lean" || tpl.LayerVersion == h.s.layerVersion(DefaultProfile()) {
		t.Fatalf("template = %+v, want lean with its own layer version", tpl)
	}
	spec, err := h.s.BuildSpec(ctx, tpl.BuildEnvID)
	if err != nil || strings.Join(spec.Remove, ",") != "nodejs" || strings.Join(spec.RemoveReport, ",") != "Node.js,Npm" {
		t.Fatalf("spec = %+v, %v, want nodejs left out with its report lines", spec, err)
	}
	var layerTar bytes.Buffer
	if err := h.s.WriteLayer(ctx, tpl.BuildEnvID, &layerTar); err != nil {
		t.Fatal(err)
	}
	script := tarFile(t, layerTar.Bytes(), "profile.sh")
	for _, want := range []string{"apt-get install -y --no-install-recommends 'zip'", "/tmp/ghrm-profile/toolcache.sh 'go' '1.24'",
		`chmod -R a+rwX "$AGENT_TOOLSDIRECTORY"`, "bash -eo pipefail /tmp/ghrm-profile/script.sh </dev/null"} {
		if !strings.Contains(script, want) {
			t.Errorf("profile.sh lacks %q:\n%s", want, script)
		}
	}
	if user := tarFile(t, layerTar.Bytes(), "script.sh"); user != "echo 'hi'\n" {
		t.Errorf("script.sh = %q", user)
	}
	// The profile's differences are explained, so the version activates.
	got := h.buildToVerifyFrom(t, tpl)
	if err := h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(leanReport)); err != nil {
		t.Fatal(err)
	}
	h.s.Wait()
	if done := h.state(t, tpl.ID); done.State != store.TemplateActive {
		t.Fatalf("lean version = %s (report %s), want active", done.State, done.Report)
	}
	// Scale sets of lean clone it; the default profile keeps the bootstrap template.
	if ref, _ := h.s.Active(ctx, "lean"); ref == "" {
		t.Fatal("lean must clone its own template")
	}
	if ref, vmid := h.s.Active(ctx, "default"); ref != "" || vmid != 949 {
		t.Fatalf("default = %q %d, want the bootstrap template", ref, vmid)
	}
}

func TestAProfileWithoutATemplateClonesTheDefaultOne(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	def := h.buildActive(t)
	_ = h.s.PutProfile(ctx, lean)
	ref, vmid := h.s.Active(ctx, "lean")
	if ref == "" || vmid != def.VMID {
		t.Fatalf("lean before its build = %q %d, want the default profile's %d", ref, vmid, def.VMID)
	}
}

func TestTheCheckerBuildsANewOrChangedProfile(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	h.buildActive(t)
	h.s.Tick(ctx) // the default profile is up to date
	before, _ := h.db.ListTemplates(ctx)
	_ = h.s.PutProfile(ctx, lean) // a new profile is checked at once, not after the interval
	h.s.Tick(ctx)
	list, _ := h.db.ListTemplates(ctx)
	if len(list) != len(before)+1 || list[0].Profile != "lean" || list[0].Trigger != "new-profile" {
		t.Fatalf("after adding lean = %+v", list[0])
	}
	got := h.buildToVerifyFrom(t, list[0])
	_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(leanReport))
	h.s.Wait()
	changed := lean
	changed.Apt = []string{"zip", "unzip"}
	_ = h.s.PutProfile(ctx, changed)
	h.s.Tick(ctx)
	list, _ = h.db.ListTemplates(ctx)
	if list[0].Profile != "lean" || list[0].Trigger != "layer" {
		t.Fatalf("after changing lean = %+v", list[0])
	}
}

func TestDeletingAProfileRetiresItsTemplates(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	_ = h.s.PutProfile(ctx, lean)
	tpl, _ := h.s.Build(ctx, "manual", "lean")
	got := h.buildToVerifyFrom(t, tpl)
	_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(leanReport))
	h.s.Wait()
	if err := h.s.DeleteProfile(ctx, "lean"); err != nil {
		t.Fatal(err)
	}
	if st := h.state(t, tpl.ID).State; st != store.TemplateRetired && st != store.TemplateDeleted {
		t.Fatalf("lean's template = %s, want retired", st)
	}
	if _, err := h.s.Profile(ctx, "lean"); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("profile after delete = %v", err)
	}
	if err := h.s.DeleteProfile(ctx, "default"); !errors.Is(err, ErrDefaultProfile) {
		t.Fatalf("deleting the default profile = %v", err)
	}
	if err := h.s.PutProfile(ctx, Profile{Name: "bad", Remove: []string{"docker-cli"}}); err == nil {
		t.Fatal("an invalid profile must be refused")
	}
}

func TestPinsHoldOnlyTheirProfile(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	def := h.buildActive(t)
	if err := h.s.Pin(ctx, def.ID, true); err != nil {
		t.Fatal(err)
	}
	_ = h.s.PutProfile(ctx, lean)
	tpl, _ := h.s.Build(ctx, "manual", "lean")
	got := h.buildToVerifyFrom(t, tpl)
	_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(leanReport))
	h.s.Wait()
	if st := h.state(t, tpl.ID).State; st != store.TemplateActive {
		t.Fatalf("lean = %s, want active: the default profile's pin is not lean's", st)
	}
}

func TestExplainProfile(t *testing.T) {
	rep, err := CompareReports([]byte(published), []byte(leanReport), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unexpected == 0 {
		t.Fatal("without the profile the missing Python is unexpected")
	}
	rep.ExplainProfile(lean)
	if rep.Unexpected != 0 {
		t.Fatalf("with the profile: %+v", rep.Differences)
	}
	for _, d := range rep.Differences {
		if strings.HasSuffix(d.Name, "Node.js") && d.Reason != "left out by the template profile" {
			t.Fatalf("node = %+v", d)
		}
	}
}

func tarFile(t *testing.T, b []byte, name string) string {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("%s not in the tar: %v", name, err)
		}
		if h.Name == name {
			c, _ := io.ReadAll(tr)
			return string(c)
		}
	}
}

// Each tool a component installs is a line of GitHub's report script (an excerpt of
// images/ubuntu-slim/scripts/docs-gen/Generate-SoftwareReport.ps1), which the self-test drops.
func TestComponentToolsAreInGitHubsReportScript(t *testing.T) {
	b, err := os.ReadFile("testdata/report-tools.ps1")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range Components {
		for _, name := range c.Report {
			if !strings.Contains(string(b), `.AddToolVersion("`+name+`",`) {
				t.Errorf("%s: %q is not in the report script", c.ID, name)
			}
		}
	}
}

func TestAVersionKeepsTheProfileItWasBuiltFrom(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	_ = h.s.PutProfile(ctx, lean)
	tpl, _ := h.s.Build(ctx, "manual", "lean")
	changed := lean
	changed.Remove = []string{"yq"}
	_ = h.s.PutProfile(ctx, changed) // edited while the version builds
	spec, err := h.s.BuildSpec(ctx, tpl.BuildEnvID)
	if err != nil || strings.Join(spec.Remove, ",") != "nodejs" {
		t.Fatalf("spec = %+v, %v, want the profile as it was when the build started", spec, err)
	}
	if err := h.s.DeleteProfile(ctx, "lean"); !errors.Is(err, ErrProfileBuilding) {
		t.Fatalf("deleting a profile while it builds = %v, want ErrProfileBuilding", err)
	}
}

func TestTheCheckerBuildsTheNextProfileWhenABuildEnds(t *testing.T) {
	h := newService(t, func(c *config.Templates) { c.VMIDRange = config.VMIDRange{Start: 950, End: 970} })
	ctx := context.Background()
	h.buildActive(t)
	_ = h.s.PutProfile(ctx, lean)
	other := lean
	other.Name = "other"
	_ = h.s.PutProfile(ctx, other)
	h.s.Tick(ctx)
	first, _ := h.db.ListTemplates(ctx)
	if first[0].State != store.TemplateBuilding {
		t.Fatalf("first tick = %+v", first[0])
	}
	got := h.buildToVerifyFrom(t, first[0])
	_ = h.s.ReceiveSelfTest(ctx, got.VerifyEnvID, okReport(leanReport))
	h.s.Wait()
	h.s.Tick(ctx) // no 24 h wait: the build ended, the other profile is due
	second, _ := h.db.ListTemplates(ctx)
	if second[0].State != store.TemplateBuilding || second[0].Profile == first[0].Profile {
		t.Fatalf("second tick = %+v, want the other profile building", second[0])
	}
}

func TestTheVMIDRangeLimitsTheProfiles(t *testing.T) {
	h := newService(t, nil) // 950-958: nine VMIDs
	ctx := context.Background()
	if err := h.s.PutProfile(ctx, lean); err != nil {
		t.Fatalf("a second profile = %v", err)
	}
	third := lean
	third.Name = "third"
	if err := h.s.PutProfile(ctx, third); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("a third profile in nine VMIDs = %v, want ErrNoRoom", err)
	}
	if err := h.s.PutProfile(ctx, lean); err != nil {
		t.Fatalf("changing an existing profile = %v", err)
	}
}

func TestAnUnreadableProfileIsReported(t *testing.T) {
	h := newService(t, nil)
	ctx := context.Background()
	_ = h.db.PutTemplateProfile(ctx, "broken", []byte("{"))
	if ps, _ := h.s.Profiles(ctx); len(ps) != 1 {
		t.Fatalf("profiles = %+v, want only the default one", ps)
	}
	evs, _ := h.db.ListEvents(ctx, store.EventFilter{Limit: 100})
	found := false
	for _, e := range evs {
		found = found || e.Kind == "template.profile_invalid"
	}
	if !found {
		t.Fatal("an unreadable profile must be reported")
	}
}

func TestARetiredVersionCountsUntilTheProfileIsSavedAgain(t *testing.T) {
	now := time.Now()
	p := Profile{Name: "lean", SavedAt: now.Add(-time.Hour)}
	list := []store.Template{{Profile: "lean", SlimRelease: "s", RunnerVersion: "r", LayerVersion: "l", State: store.TemplateRetired, UpdatedAt: now.Add(-time.Minute)}}
	if !triedAlready(list, p, "s", "r", "l", now) {
		t.Fatal("a version retired by a roll-back must not be rebuilt")
	}
	p.SavedAt = now
	if triedAlready(list, p, "s", "r", "l", now) {
		t.Fatal("a profile saved after the version was retired builds again")
	}
}

func TestAptPackagesMayNameAnArchitectureOrAVersion(t *testing.T) {
	p := Profile{Name: "x", Apt: []string{"libc6:i386", "zip=3.0-13"}}
	if err := p.Normalize().Validate(); err != nil {
		t.Fatal(err)
	}
}
