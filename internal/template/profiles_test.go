package template

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cocardoso/gh-runners-manager/internal/store"
)

var lean = Profile{Name: "lean", Remove: []string{"python"}, Apt: []string{"zip"}, Toolcache: map[string][]string{"go": {"1.24"}}, Script: "echo 'hi'"}

// The software report of a template built from lean: no Python, zip installed.
const leanReport = `{"NodeType":"HeaderNode","Title":"Ubuntu-Slim","Children":[
 {"NodeType":"ToolVersionNode","ToolName":"OS Version:","Version":"24.04.3 LTS"},
 {"NodeType":"ToolVersionNode","ToolName":"Image Version:","Version":"20261005.17.1"},
 {"NodeType":"HeaderNode","Title":"Installed Software","Children":[
   {"NodeType":"HeaderNode","Title":"Language and Runtime","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Node.js","Version":"24.13.0"}]},
   {"NodeType":"HeaderNode","Title":"Tools","Children":[
     {"NodeType":"ToolVersionNode","ToolName":"Git","Version":"2.51.0"},
     {"NodeType":"ToolVersionsListNode","ToolName":"Cached Tools","Versions":["1.2","1.3"]},
     {"NodeType":"TableNode","Headers":"Name|Version","Rows":["curl|8.5.0","jq|1.7.1","zip|3.0"]}]}]}]}`

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
	if err != nil || strings.Join(spec.Remove, ",") != "python" {
		t.Fatalf("spec = %+v, %v, want python left out", spec, err)
	}
	var layerTar bytes.Buffer
	if err := h.s.WriteLayer(ctx, tpl.BuildEnvID, &layerTar); err != nil {
		t.Fatal(err)
	}
	script := tarFile(t, layerTar.Bytes(), "profile.sh")
	for _, want := range []string{"apt-get install -y --no-install-recommends 'zip'", "/tmp/ghrm-profile/toolcache.sh 'go' '1.24'", "echo 'hi'"} {
		if !strings.Contains(script, want) {
			t.Errorf("profile.sh lacks %q:\n%s", want, script)
		}
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
		if strings.HasSuffix(d.Name, "Python") && d.Reason != "left out by the template profile" {
			t.Fatalf("python = %+v", d)
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
