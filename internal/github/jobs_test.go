package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJobDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/o/r/actions/runs/7/jobs":
			_, _ = w.Write([]byte(`{"jobs":[
				{"id":1,"name":"other","runner_name":"ghrm-zzz","html_url":"u1","status":"completed","conclusion":"success","steps":[]},
				{"id":2,"name":"build","runner_name":"ghrm-abc","html_url":"https://github.com/o/r/actions/runs/7/job/2","status":"completed","conclusion":"failure",
				 "steps":[{"number":1,"name":"Set up job","status":"completed","conclusion":"success","started_at":"2026-10-07T10:00:00Z","completed_at":"2026-10-07T10:00:01Z"},
				          {"number":2,"name":"Run tests","status":"completed","conclusion":"failure"}]}]}`))
		case "/repos/o/denied/actions/runs/7/jobs":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	rest := &REST{BaseURL: srv.URL, HTTP: srv.Client()}

	j, err := rest.JobDetails(context.Background(), "tok", "o/r", 7, "ghrm-abc")
	if err != nil || !j.Available || j.ID != 2 || len(j.Steps) != 2 || j.Steps[1].Conclusion != "failure" || !strings.Contains(j.URL, "/job/2") {
		t.Fatalf("details = %+v, %v", j, err)
	}
	j, err = rest.JobDetails(context.Background(), "tok", "o/denied", 7, "ghrm-abc")
	if err != nil || j.Available || !strings.Contains(j.Reason, "Actions: read") {
		t.Fatalf("forbidden = %+v, %v; want available=false with a permission hint", j, err)
	}
	j, err = rest.JobDetails(context.Background(), "tok", "o/r", 7, "ghrm-missing")
	if err != nil || j.Available || !strings.Contains(j.Reason, "not found") {
		t.Fatalf("missing = %+v, %v", j, err)
	}
}

func TestJobDetailsEmptyStepsAndRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/limited/actions/runs/7/jobs":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		case "/repos/o/denied/actions/runs/7/jobs":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	rest := &REST{BaseURL: srv.URL, HTTP: srv.Client()}
	for _, repo := range []string{"o/denied", "o/gone"} {
		j, err := rest.JobDetails(context.Background(), "tok", repo, 7, "x")
		if err != nil || j.Steps == nil {
			t.Fatalf("%s: steps = nil (%+v, %v); want an empty list so the API answers []", repo, j, err)
		}
	}
	j, err := rest.JobDetails(context.Background(), "tok", "o/limited", 7, "x")
	if err != nil || j.Available || !strings.Contains(j.Reason, "rate limit") || strings.Contains(j.Reason, "Actions: read") {
		t.Fatalf("rate limited = %+v, %v; want a rate-limit reason, not a permission hint", j, err)
	}
}
