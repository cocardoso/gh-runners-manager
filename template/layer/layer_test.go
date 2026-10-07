package layer

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestTarHoldsTheLayerAndTheAgent(t *testing.T) {
	var buf bytes.Buffer
	agent := []byte("\x7fELF agent")
	if err := Tar(&buf, bytes.NewReader(agent), int64(len(agent))); err != nil {
		t.Fatal(err)
	}
	got := map[string]*tar.Header{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = h
		if h.Name == "ghrm-agent" {
			b, _ := io.ReadAll(tr)
			if !bytes.Equal(b, agent) {
				t.Fatal("agent bytes differ")
			}
		}
	}
	for _, name := range []string{"Dockerfile", "ghrm-agent.service", "apt-ipv4.conf", "ghrm-agent"} {
		if got[name] == nil {
			t.Fatalf("missing %s in %v", name, got)
		}
	}
	if got["ghrm-agent"].Mode != 0o755 {
		t.Fatalf("agent mode = %o", got["ghrm-agent"].Mode)
	}
}

func TestDockerfileFollowsTheLayerRules(t *testing.T) {
	b, err := fs.ReadFile(Files(), "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	df := string(b)
	for _, want := range []string{
		"sha256sum -c",          // the runner download is verified
		"docker-ce",             // Docker Engine (the slim image has only the CLI)
		"systemd-sysv",          // systemd as init
		"NOPASSWD",              // passwordless sudo for the runner user
		"LANG=C.UTF-8",          // locale (spike finding)
		"machine-id",            // unique per clone
		"ssh_host_",             // unique per clone
		"/run/.containerenv",    // the slim build marks itself as a container
		"/etc/environment",      // image ENV survives docker export
		"installdependencies.sh",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile lacks %q", want)
		}
	}
	if regexp.MustCompile(`\|\s*(sudo\s+)?(ba)?sh\b`).MatchString(df) {
		t.Error("Dockerfile must not pipe downloads into a shell")
	}
	for _, bad := range []string{"curl -k", "--insecure"} {
		if strings.Contains(df, bad) {
			t.Errorf("Dockerfile must not contain %q", bad)
		}
	}
	unit, _ := fs.ReadFile(Files(), "ghrm-agent.service")
	if !strings.Contains(string(unit), "ExecStart=/usr/local/bin/ghrm-agent") {
		t.Fatalf("unit = %s", unit)
	}
}
