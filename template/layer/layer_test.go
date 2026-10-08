package layer

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTarHoldsTheLayerAndTheAgent(t *testing.T) {
	var buf bytes.Buffer
	agent := []byte("\x7fELF agent")
	if err := Tar(&buf, bytes.NewReader(agent), int64(len(agent)), "#!/bin/bash\necho profile\n"); err != nil {
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
	for _, name := range []string{"Dockerfile", "ghrm-agent.service", "apt-ipv4.conf", "persist-env.sh", "mirrors.sh", "toolcache.sh", "profile.sh", "ghrm-agent"} {
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
		"sha256sum -c",       // the runner download is verified
		"docker-ce",          // Docker Engine (the slim image has only the CLI)
		"systemd-sysv",       // systemd as init
		"NOPASSWD",           // passwordless sudo for the runner user
		"LANG=C.UTF-8",       // locale
		"machine-id",         // unique per clone
		"ssh_host_",          // unique per clone
		"/run/.containerenv", // the slim build marks itself as a container
		"/etc/environment",   // image ENV survives docker export
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

// The slim image's own scripts write ImageOS= (empty) and ImageVersion=1.0.0 into
// /etc/environment; jobs on hosted runners see the container's ENV instead, so the
// image's ENV must win.
func TestPersistEnvLetsTheImageEnvWin(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	script, err := fs.ReadFile(Files(), "persist-env.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	scriptPath, envPath := filepath.Join(dir, "persist-env.sh"), filepath.Join(dir, "environment")
	_ = os.WriteFile(scriptPath, script, 0o755)
	_ = os.WriteFile(envPath, []byte("PATH=/usr/bin:/bin\nImageVersion=1.0.0\nImageOS=\nLANG=en_US.UTF-8\nNVM_DIR=$HOME/.nvm\nEMPTIED=x\n"), 0o644)
	cmd := exec.Command(bash, scriptPath, envPath, "ImageVersion", "ImageOS", "IMAGE_OWNER", "UNSET_VAR", "EMPTIED")
	// An ENV set to an empty value is empty in a container too, so it wins as well.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "ImageVersion=20260925.9", "ImageOS=Linux", "IMAGE_OWNER=GitHub", "EMPTIED="}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	b, _ := os.ReadFile(envPath)
	got := string(b)
	for _, want := range []string{"PATH=/usr/bin:/bin\n", "NVM_DIR=$HOME/.nvm\n", "ImageVersion=20260925.9\n", "ImageOS=Linux\n", "IMAGE_OWNER=GitHub\n", "LANG=C.UTF-8\n", "EMPTIED=\n"} {
		if strings.Count(got, want) != 1 {
			t.Errorf("want exactly one %q in:\n%s", want, got)
		}
	}
	for _, gone := range []string{"ImageVersion=1.0.0", "ImageOS=\n", "en_US", "UNSET_VAR", "EMPTIED=x"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q must be gone:\n%s", gone, got)
		}
	}
}

func runMirrors(t *testing.T, list string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	script, err := fs.ReadFile(Files(), "mirrors.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	_ = os.MkdirAll(root, 0o755)
	scriptPath := filepath.Join(dir, "mirrors.sh")
	_ = os.WriteFile(scriptPath, script, 0o755)
	if out, err := exec.Command(bash, scriptPath, root, list).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return root
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return string(b)
}

// Jobs pull through the cache without workflow changes: the Docker daemon (Docker Hub),
// containerd (the other registries) and BuildKit (buildx builders) are all configured.
func TestMirrorsScriptWritesDaemonContainerdAndBuildkitConfig(t *testing.T) {
	root := runMirrors(t, "docker.io=10.50.0.3:5000,ghcr.io=10.50.0.3:5001,mcr.microsoft.com=10.50.0.3:5002,quay.io=10.50.0.3:5003")
	daemon := read(t, filepath.Join(root, "etc/docker/daemon.json"))
	for _, want := range []string{`"registry-mirrors": ["http://10.50.0.3:5000"]`, `"10.50.0.3:5001"`, `"10.50.0.3:5003"`} {
		if !strings.Contains(daemon, want) {
			t.Errorf("daemon.json lacks %s:\n%s", want, daemon)
		}
	}
	hosts := read(t, filepath.Join(root, "etc/docker/certs.d/mcr.microsoft.com/hosts.toml"))
	if !strings.Contains(hosts, `server = "https://mcr.microsoft.com"`) || !strings.Contains(hosts, `[host."http://10.50.0.3:5002"]`) ||
		!strings.Contains(hosts, `capabilities = ["pull", "resolve"]`) {
		t.Errorf("hosts.toml:\n%s", hosts)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/docker/certs.d/docker.io")); err == nil {
		t.Error("Docker Hub goes through registry-mirrors, not hosts.toml")
	}
	bk := read(t, filepath.Join(root, "home/runner/.docker/buildx/buildkitd.default.toml"))
	for _, want := range []string{"[registry.\"docker.io\"]\n  mirrors = [\"10.50.0.3:5000\"]", "[registry.\"quay.io\"]\n  mirrors = [\"10.50.0.3:5003\"]", "[registry.\"10.50.0.3:5002\"]\n  http = true"} {
		if !strings.Contains(bk, want) {
			t.Errorf("buildkitd.default.toml lacks %q:\n%s", want, bk)
		}
	}
}

func TestMirrorsScriptConfiguresBuildkitForRootToo(t *testing.T) {
	root := runMirrors(t, "docker.io=10.50.0.3:5000")
	runner := read(t, filepath.Join(root, "home/runner/.docker/buildx/buildkitd.default.toml"))
	if got := read(t, filepath.Join(root, "root/.docker/buildx/buildkitd.default.toml")); got != runner {
		t.Fatalf("root's buildkitd.default.toml = %q, want the runner's (sudo docker buildx create)", got)
	}
}

func TestMirrorsScriptRefusesToReplaceAnExistingDaemonConfig(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	script, _ := fs.ReadFile(Files(), "mirrors.sh")
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	_ = os.MkdirAll(filepath.Join(root, "etc/docker"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "etc/docker/daemon.json"), []byte(`{"log-driver": "local"}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "mirrors.sh"), script, 0o755)
	out, err := exec.Command(bash, filepath.Join(dir, "mirrors.sh"), root, "docker.io=10.50.0.3:5000").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "daemon.json already exists") {
		t.Fatalf("err = %v, out = %s; an existing daemon.json must not be silently replaced", err, out)
	}
	if got := read(t, filepath.Join(root, "etc/docker/daemon.json")); got != `{"log-driver": "local"}` {
		t.Fatalf("daemon.json = %s", got)
	}
}

func TestMirrorsScriptWithoutCacheWritesNothing(t *testing.T) {
	root := runMirrors(t, "")
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("wrote %v; without a cache templates stay as they were", entries)
	}
}
