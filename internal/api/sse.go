package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cocardoso/gh-runners-manager/internal/logs"
	"github.com/cocardoso/gh-runners-manager/internal/store"
)

// heartbeat is how often streams send a named ping event, so clients can detect a stale connection.
var heartbeat = 15 * time.Second

// followIdleCheck is how often a log follow checks whether its environment is gone.
var followIdleCheck = 5 * time.Second

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

// events streams the global event timeline. It first sends a hello with the latest sequence,
// then resumes after Last-Event-ID (or ?after=; after=latest starts with the next event) and
// resyncs from the store whenever the in-process subscription lagged.
func (s *sse) events(w http.ResponseWriter, r *http.Request) {
	sub := s.d.Recorder.Bus().Subscribe(512) // subscribe before the backlog so nothing is missed
	defer sub.Close()
	latest, err := s.d.Store.LatestEventSeq(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Last-Event-ID (a native reconnect) wins over the query, which a reconnect repeats.
	var after int64
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	} else if v := r.URL.Query().Get("after"); v == "latest" {
		after = latest
	} else if v != "" {
		after, _ = strconv.ParseInt(v, 10, 64)
	}
	f, ok := startSSE(w)
	if !ok {
		return
	}
	// hello tells the client the newest sequence, so it can notice a store that went back in time.
	if _, err := fmt.Fprintf(w, "event: hello\ndata: {\"latest\":%d}\n\n", latest); err != nil {
		return
	}
	f.Flush()
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
			if _, err := fmt.Fprint(w, "event: ping\ndata: {}\n\n"); err != nil {
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
	if _, err := s.d.Store.GetEnvironment(r.Context(), envID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "environment not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
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
	check := time.NewTicker(followIdleCheck)
	defer check.Stop()
	lastEntry := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			f.Flush()
		case <-check.C:
			// A destroyed environment writes nothing more: end the stream once caught up.
			if time.Since(lastEntry) >= followIdleCheck {
				if e, err := s.d.Store.GetEnvironment(ctx, envID); err == nil && e.State == "destroyed" {
					_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
					f.Flush()
					return
				}
			}
		case e, ok := <-ch:
			if !ok {
				return
			}
			lastEntry = time.Now()
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
