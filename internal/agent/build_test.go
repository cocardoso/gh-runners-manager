package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/ingest"
	"github.com/klauspost/compress/zstd"
)

// fakeCommander records commands and plays scripted output.
type fakeCommander struct {
	mu      sync.Mutex
	calls   []string
	failOn  string            // a command line containing this fails
	outputs map[string]string // command prefix -> output line
	export  []byte            // stdout of "docker export"
	files   map[string]string // files to create when a command runs: prefix -> path|content
	check   func(line string) error
}

func (f *fakeCommander) line(name string, args []string) string {
	return name + " " + strings.Join(args, " ")
}

func (f *fakeCommander) Run(_ context.Context, _ string, name string, args []string, out func(string)) error {
	l := f.line(name, args)
	f.mu.Lock()
	f.calls = append(f.calls, l)
	f.mu.Unlock()
	for prefix, o := range f.outputs {
		if strings.HasPrefix(l, prefix) {
			out(o)
		}
	}
	for prefix, spec := range f.files {
		if strings.HasPrefix(l, prefix) {
			p, content, _ := strings.Cut(spec, "|")
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(content), 0o644)
		}
	}
	if f.check != nil {
		if err := f.check(l); err != nil {
			out(err.Error())
			return err
		}
	}
	if f.failOn != "" && strings.Contains(l, f.failOn) {
		out("boom: " + f.failOn)
		return errors.New("exit status 1")
	}
	return nil
}

func (f *fakeCommander) Stream(ctx context.Context, dir, name string, args []string, stdout io.Writer, out func(string)) error {
	if err := f.Run(ctx, dir, name, args, out); err != nil {
		return err
	}
	_, err := stdout.Write(f.export)
	return err
}

// fakeIngest serves the build endpoints and collects frames.
type fakeIngest struct {
	mu        sync.Mutex
	frames    []ingest.Frame
	rootfs    []byte
	rootfsSHA string
	report    *ingest.SelfTestReport
}

func (f *fakeIngest) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ingest.FramesPath, func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		f.mu.Lock()
		for {
			var fr ingest.Frame
			if dec.Decode(&fr) != nil {
				break
			}
			f.frames = append(f.frames, fr)
		}
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"accepted":1}`))
	})
	mux.HandleFunc("GET "+ingest.BuildSpecPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ingest.BuildSpec{TemplateID: "tpl1", SlimTag: "ubuntu-slim/20261005.17", RunnerVersion: "2.338.0",
			RunnerSHA256: strings.Repeat("c", 64), LayerVersion: "1"})
	})
	mux.HandleFunc("GET "+ingest.BuildLayerPath, func(w http.ResponseWriter, r *http.Request) {
		tw := tar.NewWriter(w)
		_ = tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: 4})
		_, _ = tw.Write([]byte("FROM"))
		_ = tw.Close()
	})
	mux.HandleFunc("PUT "+ingest.BuildRootFSPath, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.rootfs, f.rootfsSHA = b, r.Header.Get(ingest.HeaderSHA256)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+ingest.SelfTestPath, func(w http.ResponseWriter, r *http.Request) {
		var rep ingest.SelfTestReport
		if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
			t.Errorf("report: %v", err)
		}
		f.mu.Lock()
		f.report = &rep
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (f *fakeIngest) events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, fr := range f.frames {
		if fr.Type == ingest.TypeEvent {
			step, _ := fr.Data["step"].(string)
			out = append(out, strings.TrimSuffix(fr.Name+":"+step, ":"))
		}
	}
	return out
}

func newBuildClient(t *testing.T, fi *fakeIngest) *Client {
	srv := httptest.NewTLSServer(fi.handler(t))
	t.Cleanup(srv.Close)
	c, err := NewClient(Bootstrap{URL: srv.URL, Token: "tok", Fingerprint: ingest.Fingerprint(srv.Certificate().Raw)}, Options{Interval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	return c
}

func exportTar(t *testing.T) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range []string{".dockerenv", "etc/os-release", "run/.containerenv", "usr/bin/tool"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: 2})
		_, _ = tw.Write([]byte("ok"))
	}
	_ = tw.Close()
	return buf.Bytes()
}

func TestRunBuildBuildsTheOfficialRecipeThenTheLayer(t *testing.T) {
	fi := &fakeIngest{}
	c := newBuildClient(t, fi)
	cmd := &fakeCommander{export: exportTar(t), outputs: map[string]string{"docker create": "abc123"}}
	work := t.TempDir()
	if err := RunBuild(context.Background(), c, cmd, work); err != nil {
		t.Fatal(err)
	}
	_ = c.Flush(context.Background())
	calls := strings.Join(cmd.calls, "\n")
	for _, want := range []string{
		"git clone --depth 1 --branch ubuntu-slim/20261005.17 https://github.com/actions/runner-images " + filepath.Join(work, "runner-images"),
		"docker build --progress=plain --build-arg IMAGE_VERSION=20261005.17 -t ghrm-slim:tpl1 " + filepath.Join(work, "runner-images", "images", "ubuntu-slim"),
		"docker build --progress=plain --build-arg BASE=ghrm-slim:tpl1 --build-arg RUNNER_VERSION=2.338.0 --build-arg RUNNER_SHA256=" + strings.Repeat("c", 64) + " --build-arg LAYER_VERSION=1 -t ghrm-tpl:tpl1 " + filepath.Join(work, "layer"),
		"docker create --name ghrm-export-tpl1 ghrm-tpl:tpl1",
		"docker export ghrm-export-tpl1",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing command %q in\n%s", want, calls)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(work, "layer", "Dockerfile")); string(b) != "FROM" {
		t.Fatalf("layer not unpacked: %q", b)
	}
	sum := sha256.Sum256(fi.rootfs)
	if fi.rootfsSHA != hex.EncodeToString(sum[:]) {
		t.Fatal("uploaded SHA-256 does not match the archive")
	}
	dec, _ := zstd.NewReader(bytes.NewReader(fi.rootfs))
	tr := tar.NewReader(dec)
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "etc/os-release,usr/bin/tool" {
		t.Fatalf("archive entries = %v, want the container markers dropped", names)
	}
	ev := strings.Join(fi.events(), ",")
	if !strings.Contains(ev, "build_step:clone") || !strings.HasSuffix(ev, "build_finished") {
		t.Fatalf("events = %s", ev)
	}
}

func TestRunBuildReportsTheFailedStep(t *testing.T) {
	fi := &fakeIngest{}
	c := newBuildClient(t, fi)
	cmd := &fakeCommander{failOn: "ghrm-slim:tpl1 /"}
	err := RunBuild(context.Background(), c, cmd, t.TempDir())
	if err == nil {
		t.Fatal("want an error")
	}
	_ = c.Flush(context.Background())
	if ev := strings.Join(fi.events(), ","); !strings.Contains(ev, "build_failed") || strings.Contains(ev, "build_finished") {
		t.Fatalf("events = %s", ev)
	}
	if fi.rootfs != nil {
		t.Fatal("nothing must be uploaded after a failure")
	}
}

func TestUnsafeLayerPathsAreRejected(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	if err := untar(&buf, t.TempDir()); err == nil {
		t.Fatal("a path escaping the directory must be rejected")
	}
}

func TestOSCommanderUsesItsEnvironment(t *testing.T) {
	var lines []string
	err := OSCommander{Env: []string{"PATH=/usr/bin:/bin", "ImageVersion=20261005.17"}}.Run(context.Background(), t.TempDir(), "sh", []string{"-c", "echo v=$ImageVersion"}, func(l string) { lines = append(lines, l) })
	if err != nil || len(lines) != 1 || lines[0] != "v=20261005.17" {
		t.Fatalf("lines = %v, %v", lines, err)
	}
}
