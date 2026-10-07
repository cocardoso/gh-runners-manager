package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

const heartbeat = 15 * time.Second

type sse struct{ d Deps }

func startSSE(w http.ResponseWriter) (http.Flusher, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return nil, false
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return f, true
}

func writeEvent(w http.ResponseWriter, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", id, b)
	return err
}

// events streams the global event timeline. It resumes after Last-Event-ID (or ?after=)
// and resyncs from the store whenever the in-process subscription lagged.
func (s *sse) events(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if v := r.URL.Query().Get("after"); v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	sub := s.d.Recorder.Bus().Subscribe(512) // subscribe before the backlog so nothing is missed
	defer sub.Close()
	f, ok := startSSE(w)
	if !ok {
		return
	}
	ctx := r.Context()
	last := after
	backlog := func() bool {
		for {
			evs, err := s.d.Store.ListEvents(ctx, store.EventFilter{AfterSeq: last, Limit: 1000})
			if err != nil {
				return false
			}
			for _, e := range evs {
				if writeEvent(w, strconv.FormatInt(e.Seq, 10), e) != nil {
					return false
				}
				last = e.Seq
			}
			f.Flush()
			if len(evs) < 1000 {
				return true
			}
		}
	}
	if !backlog() {
		return
	}
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			f.Flush()
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if sub.Lagged() {
				sub.ResetLagged()
				if !backlog() {
					return
				}
				continue
			}
			if e.Seq <= last {
				continue
			}
			if writeEvent(w, strconv.FormatInt(e.Seq, 10), e) != nil {
				return
			}
			last = e.Seq
			f.Flush()
		}
	}
}

// logs tails one log stream. The SSE id is the entry's byte offset.
func (s *sse) logs(w http.ResponseWriter, r *http.Request, envID, stream string) {
	if !logs.ValidStream(stream) {
		http.Error(w, "unknown stream", http.StatusBadRequest)
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	skip := int64(-1)
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			offset, skip = n, n // resume at the last delivered entry, without repeating it
		}
	}
	f, ok := startSSE(w)
	if !ok {
		return
	}
	ctx := r.Context()
	ch := s.d.Logs.Follow(ctx, envID, stream, offset)
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			f.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Offset == skip {
				continue
			}
			if writeEvent(w, strconv.FormatInt(e.Offset, 10), e) != nil {
				return
			}
			f.Flush()
		}
	}
}
