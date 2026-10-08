package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Template build endpoints (spec §8.3, §8.4). They use the same bearer tokens as frames,
// but answer only to environments of the matching kind.
const (
	BuildSpecPath   = "/ingest/v1/build/spec"
	BuildLayerPath  = "/ingest/v1/build/layer"
	BuildAgentPath  = "/ingest/v1/build/agent"
	BuildRootFSPath = "/ingest/v1/build/rootfs"
	SelfTestPath    = "/ingest/v1/selftest"
	// HeaderSHA256 carries the hex SHA-256 of an uploaded root filesystem.
	HeaderSHA256 = "X-Ghrm-SHA256"
	// EnvMode selects the agent's mode: ModeBuild, ModeSelfTest, or absent for a job.
	EnvMode      = "GHRM_MODE"
	ModeBuild    = "build"
	ModeSelfTest = "selftest"
	// EnvSelfTestBlocked lists addresses (comma separated) that must be unreachable from a job.
	EnvSelfTestBlocked = "GHRM_SELFTEST_BLOCKED"
	// EnvSelfTestMirrors lists the registry cache's mirror addresses (comma separated)
	// a job must reach.
	EnvSelfTestMirrors = "GHRM_SELFTEST_MIRRORS"
)

// Build agent events.
const (
	EventBuildStep        = "build_step"
	EventBuildFinished    = "build_finished"
	EventBuildFailed      = "build_failed"
	EventSelfTestFinished = "selftest_finished"
)

var (
	// ErrWrongKind means the token belongs to an environment of another kind.
	ErrWrongKind = errors.New("ingest: endpoint not available to this environment")
	// ErrBadArchive means the uploaded root filesystem was rejected (checksum, size).
	ErrBadArchive = errors.New("ingest: archive rejected")
)

// BuildSpec tells a builder what to build.
type BuildSpec struct {
	TemplateID    string `json:"template_id"`
	SlimTag       string `json:"slim_tag"`
	RunnerVersion string `json:"runner_version"`
	RunnerSHA256  string `json:"runner_sha256"`
	LayerVersion  string `json:"layer_version"`
	// AgentSHA256 is the control plane's ghrm-agent; a builder running another
	// agent updates itself first (it is a clone of the active template).
	AgentSHA256 string `json:"agent_sha256,omitempty"`
	// CacheMirrors points the template at the registry cache ("origin=host:port,...";
	// empty: no cache).
	CacheMirrors string `json:"cache_mirrors,omitempty"`
}

// Check is one self-test result.
type Check struct {
	Name    string  `json:"name"`
	OK      bool    `json:"ok"`
	Detail  string  `json:"detail,omitempty"`
	Seconds float64 `json:"seconds"`
}

// SelfTestReport is what a verify environment reports.
type SelfTestReport struct {
	Checks   []Check         `json:"checks"`
	Software json.RawMessage `json:"software,omitempty"`
}

// BuildService serves template builds; internal/template implements it.
type BuildService interface {
	BuildSpec(ctx context.Context, envID string) (BuildSpec, error)
	WriteLayer(ctx context.Context, envID string, w io.Writer) error
	WriteAgent(ctx context.Context, envID string, w io.Writer) error
	ReceiveRootFS(ctx context.Context, envID string, r io.Reader, sha256 string) error
	ReceiveSelfTest(ctx context.Context, envID string, rep SelfTestReport) error
}

// Option configures the ingest server.
type Option func(*server)

// WithBuilds enables the template build endpoints.
func WithBuilds(b BuildService) Option { return func(s *server) { s.builds = b } }

var hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *server) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok && token != "" {
		if envID, found := s.resolver.Resolve(r.Context(), HashToken(token)); found {
			return envID, true
		}
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return "", false
}

func buildError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrWrongKind):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrBadArchive):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) buildSpec(w http.ResponseWriter, r *http.Request) {
	envID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	spec, err := s.builds.BuildSpec(r.Context(), envID)
	if err != nil {
		buildError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(spec)
}

func (s *server) buildLayer(w http.ResponseWriter, r *http.Request) {
	envID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	// Check the kind before writing a body, so an error can still set the status.
	if _, err := s.builds.BuildSpec(r.Context(), envID); err != nil {
		buildError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	_ = s.builds.WriteLayer(r.Context(), envID, w)
}

func (s *server) buildAgent(w http.ResponseWriter, r *http.Request) {
	envID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	if _, err := s.builds.BuildSpec(r.Context(), envID); err != nil {
		buildError(w, err)
		return
	}
	// Headers go out with the first byte, so an error before it still sets the status.
	lw := &lazyWriter{w: w, contentType: "application/octet-stream"}
	if err := s.builds.WriteAgent(r.Context(), envID, lw); err != nil && !lw.wrote {
		buildError(w, err)
	}
}

// lazyWriter sets the content type when the first byte is written.
type lazyWriter struct {
	w           http.ResponseWriter
	contentType string
	wrote       bool
}

func (l *lazyWriter) Write(p []byte) (int, error) {
	if !l.wrote {
		l.w.Header().Set("Content-Type", l.contentType)
		l.wrote = true
	}
	return l.w.Write(p)
}

func (s *server) buildRootFS(w http.ResponseWriter, r *http.Request) {
	envID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	sum := strings.ToLower(r.Header.Get(HeaderSHA256))
	if !hexSHA256.MatchString(sum) {
		http.Error(w, "missing or invalid "+HeaderSHA256+" header", http.StatusBadRequest)
		return
	}
	if err := s.builds.ReceiveRootFS(r.Context(), envID, r.Body, sum); err != nil {
		buildError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) selfTest(w http.ResponseWriter, r *http.Request) {
	envID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var rep SelfTestReport
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&rep); err != nil {
		http.Error(w, "malformed report: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.builds.ReceiveSelfTest(r.Context(), envID, rep); err != nil {
		buildError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
