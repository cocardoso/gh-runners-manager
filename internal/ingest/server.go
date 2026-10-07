package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/events"
	"github.com/cocardoso/gh-runners-manager/internal/logs"
)

// TokenResolver maps a token hash to a live environment ID.
type TokenResolver interface {
	Resolve(ctx context.Context, tokenHash string) (envID string, ok bool)
}

// EventSink receives agent lifecycle events (each delivered once).
type EventSink interface {
	AgentEvent(ctx context.Context, envID, name string, at time.Time, data map[string]any)
}

type server struct {
	resolver TokenResolver
	sink     EventSink
	logs     *logs.Store
	recorder *events.Recorder
	builds   BuildService
}

// NewServer returns the ingest HTTP handler.
func NewServer(resolver TokenResolver, sink EventSink, logStore *logs.Store, recorder *events.Recorder, opts ...Option) http.Handler {
	s := &server{resolver: resolver, sink: sink, logs: logStore, recorder: recorder}
	for _, o := range opts {
		o(s)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+FramesPath, s.frames)
	if s.builds != nil {
		mux.HandleFunc("GET "+BuildSpecPath, s.buildSpec)
		mux.HandleFunc("GET "+BuildLayerPath, s.buildLayer)
		mux.HandleFunc("GET "+BuildAgentPath, s.buildAgent)
		mux.HandleFunc("PUT "+BuildRootFSPath, s.buildRootFS)
		mux.HandleFunc("POST "+SelfTestPath, s.selfTest)
	}
	return mux
}

type apiError struct {
	code int
	msg  string
}

func (s *server) frames(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	envID, found := "", false
	if ok && token != "" {
		envID, found = s.resolver.Resolve(r.Context(), HashToken(token))
	}
	if !found {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	frames, aerr := parse(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if aerr != nil {
		if aerr.code == http.StatusBadRequest && s.recorder != nil {
			_, _ = s.recorder.Warn(r.Context(), "ingest.rejected", "rejected a malformed batch from the agent",
				events.Refs{EnvironmentID: envID}, map[string]any{"reason": aerr.msg})
		}
		http.Error(w, aerr.msg, aerr.code)
		return
	}
	// Finish storing even if the agent gives up on the request: a half-stored batch
	// would lose an event or duplicate lines when it is retried.
	accepted, err := s.store(context.WithoutCancel(r.Context()), envID, frames)
	if err != nil {
		http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"accepted": accepted})
}

// parse decodes and validates the whole batch before anything is written.
func parse(body io.Reader) ([]Frame, *apiError) {
	raw, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, &apiError{http.StatusRequestEntityTooLarge, "body too large"}
		}
		return nil, &apiError{http.StatusBadRequest, err.Error()}
	}
	var frames []Frame
	for n, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var f Frame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			return nil, &apiError{http.StatusBadRequest, fmt.Sprintf("line %d: %v", n+1, err)}
		}
		if err := validate(f); err != nil {
			return nil, &apiError{http.StatusBadRequest, fmt.Sprintf("line %d: %v", n+1, err)}
		}
		frames = append(frames, f)
	}
	return frames, nil
}

func validate(f Frame) error {
	if f.Seq <= 0 {
		return errors.New("seq must be positive")
	}
	switch f.Type {
	case TypeLog:
		if !slices.Contains(AgentStreams, f.Stream) {
			return fmt.Errorf("stream %q is not writable by agents", f.Stream)
		}
	case TypeEvent:
		if f.Name == "" {
			return errors.New("event without a name")
		}
	case TypeMetric:
	default:
		return fmt.Errorf("unknown frame type %q", f.Type)
	}
	return nil
}

// store writes frames in order. Log lines are grouped per stream; an event is
// written to the "agent" stream (sharing its sequence) and reaches the sink only
// when that line is new.
func (s *server) store(ctx context.Context, envID string, frames []Frame) (int, error) {
	pending := map[string][]logs.Line{}
	accepted := 0
	flush := func(stream string) error {
		if len(pending[stream]) == 0 {
			return nil
		}
		n, err := s.logs.Append(ctx, envID, stream, pending[stream])
		accepted += n
		delete(pending, stream)
		return err
	}
	for _, f := range frames {
		switch f.Type {
		case TypeLog:
			pending[f.Stream] = append(pending[f.Stream], logs.Line{Seq: f.Seq, Time: f.Time, Text: f.Text})
		case TypeMetric:
			text, _ := json.Marshal(map[string]int64{"cpu_usec": f.CPUUsec, "mem_bytes": f.MemBytes})
			pending["metrics"] = append(pending["metrics"], logs.Line{Seq: f.Seq, Time: f.Time, Text: string(text)})
		case TypeEvent:
			if err := flush("agent"); err != nil {
				return accepted, err
			}
			data, _ := json.Marshal(f.Data)
			n, err := s.logs.Append(ctx, envID, "agent", []logs.Line{{Seq: f.Seq, Time: f.Time, Text: "event " + f.Name + " " + string(data)}})
			if err != nil {
				return accepted, err
			}
			accepted += n
			if n == 1 {
				s.sink.AgentEvent(ctx, envID, f.Name, f.Time, f.Data)
			}
		}
	}
	for stream := range pending {
		if err := flush(stream); err != nil {
			return accepted, err
		}
	}
	return accepted, nil
}
