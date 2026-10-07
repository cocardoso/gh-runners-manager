package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeBuilds struct {
	rootfsBytes int64
	rootfsSHA   string
	maxChunk    int
	report      SelfTestReport
}

func (f *fakeBuilds) BuildSpec(_ context.Context, envID string) (BuildSpec, error) {
	if envID != "bld" {
		return BuildSpec{}, ErrWrongKind
	}
	return BuildSpec{TemplateID: "t1", SlimTag: "ubuntu-slim/20261005.17", RunnerVersion: "2.338.0", RunnerSHA256: strings.Repeat("a", 64), LayerVersion: "1"}, nil
}

func (f *fakeBuilds) WriteLayer(_ context.Context, envID string, w io.Writer) error {
	if envID != "bld" {
		return ErrWrongKind
	}
	_, err := w.Write([]byte("layer-tar"))
	return err
}

func (f *fakeBuilds) ReceiveRootFS(_ context.Context, envID string, r io.Reader, sha string) error {
	if envID != "bld" {
		return ErrWrongKind
	}
	h := sha256.New()
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > f.maxChunk {
			f.maxChunk = n
		}
		h.Write(buf[:n])
		f.rootfsBytes += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return fmt.Errorf("%w: SHA-256 is %s", ErrBadArchive, got)
	}
	f.rootfsSHA = sha
	return nil
}

func (f *fakeBuilds) ReceiveSelfTest(_ context.Context, envID string, rep SelfTestReport) error {
	if envID != "vfy" {
		return ErrWrongKind
	}
	f.report = rep
	return nil
}

func buildHarness(t *testing.T) (*httptest.Server, *fakeBuilds, map[string]string) {
	tokens := map[string]string{}
	res := fakeResolver{}
	for _, env := range []string{"bld", "vfy", "job"} {
		tok, _ := NewToken()
		tokens[env] = tok
		res[HashToken(tok)] = env
	}
	b := &fakeBuilds{}
	srv := httptest.NewServer(NewServer(res, &fakeSink{}, nil, nil, WithBuilds(b)))
	t.Cleanup(srv.Close)
	return srv, b, tokens
}

func call(t *testing.T, method, url, token string, body io.Reader, hdr map[string]string) *http.Response {
	req, _ := http.NewRequest(method, url, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestBuildEndpointsAuthenticate(t *testing.T) {
	srv, _, tok := buildHarness(t)
	for _, p := range []string{BuildSpecPath, BuildLayerPath} {
		if r := call(t, "GET", srv.URL+p, "", nil, nil); r.StatusCode != 401 {
			t.Errorf("%s without token = %d", p, r.StatusCode)
		}
		if r := call(t, "GET", srv.URL+p, tok["job"], nil, nil); r.StatusCode != 403 {
			t.Errorf("%s with a job token = %d, want 403", p, r.StatusCode)
		}
	}
	r := call(t, "GET", srv.URL+BuildSpecPath, tok["bld"], nil, nil)
	var spec BuildSpec
	_ = json.NewDecoder(r.Body).Decode(&spec)
	if r.StatusCode != 200 || spec.SlimTag != "ubuntu-slim/20261005.17" {
		t.Fatalf("spec = %d %+v", r.StatusCode, spec)
	}
	r = call(t, "GET", srv.URL+BuildLayerPath, tok["bld"], nil, nil)
	b, _ := io.ReadAll(r.Body)
	if string(b) != "layer-tar" {
		t.Fatalf("layer = %q", b)
	}
}

func TestRootFSIsStreamed(t *testing.T) {
	srv, fb, tok := buildHarness(t)
	body := bytes.Repeat([]byte("0123456789abcdef"), 4<<20) // 64 MiB, beyond MaxBodyBytes
	sum := sha256.Sum256(body)
	r := call(t, "PUT", srv.URL+BuildRootFSPath, tok["bld"], bytes.NewReader(body), map[string]string{"X-Ghrm-SHA256": hex.EncodeToString(sum[:])})
	if r.StatusCode != 204 || fb.rootfsBytes != int64(len(body)) || fb.maxChunk > 1<<20 {
		t.Fatalf("status %d, %d bytes, largest read %d", r.StatusCode, fb.rootfsBytes, fb.maxChunk)
	}
	r = call(t, "PUT", srv.URL+BuildRootFSPath, tok["bld"], strings.NewReader("abc"), map[string]string{"X-Ghrm-SHA256": strings.Repeat("0", 64)})
	if r.StatusCode != 422 {
		t.Fatalf("bad checksum = %d, want 422", r.StatusCode)
	}
	r = call(t, "PUT", srv.URL+BuildRootFSPath, tok["bld"], strings.NewReader("abc"), nil)
	if r.StatusCode != 400 {
		t.Fatalf("missing checksum = %d, want 400", r.StatusCode)
	}
}

func TestSelfTestReport(t *testing.T) {
	srv, fb, tok := buildHarness(t)
	rep := `{"checks":[{"name":"docker hello-world","ok":true,"seconds":1.5}],"software":{"NodeType":"HeaderNode"}}`
	if r := call(t, "POST", srv.URL+SelfTestPath, tok["vfy"], strings.NewReader(rep), nil); r.StatusCode != 204 {
		t.Fatalf("self-test = %d", r.StatusCode)
	}
	if len(fb.report.Checks) != 1 || !fb.report.Checks[0].OK || !strings.Contains(string(fb.report.Software), "HeaderNode") {
		t.Fatalf("report = %+v", fb.report)
	}
	if r := call(t, "POST", srv.URL+SelfTestPath, tok["vfy"], strings.NewReader("{"), nil); r.StatusCode != 400 {
		t.Fatalf("malformed = %d", r.StatusCode)
	}
	if r := call(t, "POST", srv.URL+SelfTestPath, tok["bld"], strings.NewReader(rep), nil); r.StatusCode != 403 {
		t.Fatalf("wrong kind = %d", r.StatusCode)
	}
}

func TestBuildEndpointsAbsentWithoutService(t *testing.T) {
	res := fakeResolver{}
	tok, _ := NewToken()
	res[HashToken(tok)] = "bld"
	srv := httptest.NewServer(NewServer(res, &fakeSink{}, nil, nil))
	defer srv.Close()
	if r := call(t, "GET", srv.URL+BuildSpecPath, tok, nil, nil); r.StatusCode != 404 {
		t.Fatalf("without a build service = %d, want 404", r.StatusCode)
	}
	_ = errors.New
}
