package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTargetsListsRepositoriesAndTheirOrganizations(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/memberships/orgs" {
			fmt.Fprint(w, `[{"role":"admin","organization":{"login":"acme"}}]`)
			return
		}
		if r.URL.Path != "/user/repos" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/user/repos?page=2&per_page=100>; rel="next"`, srv.URL))
			fmt.Fprint(w, `[{"full_name":"cocardoso/zeropaper","name":"zeropaper","private":true,"html_url":"https://github.com/cocardoso/zeropaper","owner":{"login":"cocardoso","type":"User"},"permissions":{"admin":true}},
				{"full_name":"acme/api","name":"api","private":false,"html_url":"https://github.com/acme/api","owner":{"login":"acme","type":"Organization"},"permissions":{"admin":true}}]`)
		case "2":
			fmt.Fprint(w, `[{"full_name":"acme/web","name":"web","private":true,"html_url":"https://github.com/acme/web","owner":{"login":"acme","type":"Organization"},"permissions":{"admin":true}}]`)
		}
	}))
	defer srv.Close()
	got, err := (&REST{BaseURL: srv.URL}).Targets(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, g := range got.Targets {
		lines = append(lines, fmt.Sprintf("%s %s %s %v", g.Kind, g.FullName, g.URL, g.Private))
	}
	want := []string{
		"organization acme https://github.com/acme false",
		"repository acme/api https://github.com/acme/api false",
		"repository acme/web https://github.com/acme/web true",
		"repository cocardoso/zeropaper https://github.com/cocardoso/zeropaper true",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("targets:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestTargetsReportsGitHubsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := (&REST{BaseURL: srv.URL}).Targets(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestTargetsOnlyOfferWhatTheTokenCanRegisterRunnersFor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user/repos":
			fmt.Fprint(w, `[{"full_name":"acme/api","name":"api","html_url":"https://github.example/acme/api","owner":{"login":"acme","type":"Organization"},"permissions":{"admin":true}},
				{"full_name":"acme/docs","name":"docs","html_url":"https://github.example/acme/docs","owner":{"login":"acme","type":"Organization"},"permissions":{"admin":false,"push":true}},
				{"full_name":"other/lib","name":"lib","html_url":"https://github.example/other/lib","owner":{"login":"other","type":"Organization"},"permissions":{"admin":true}}]`)
		case "/user/memberships/orgs":
			fmt.Fprint(w, `[{"role":"admin","organization":{"login":"acme"}},{"role":"member","organization":{"login":"other"}}]`)
		}
	}))
	defer srv.Close()
	got, err := (&REST{BaseURL: srv.URL}).Targets(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, g := range got.Targets {
		lines = append(lines, g.Kind+" "+g.FullName+" "+g.URL)
	}
	want := "organization acme https://github.example/acme\nrepository acme/api https://github.example/acme/api\nrepository other/lib https://github.example/other/lib"
	if strings.Join(lines, "\n") != want {
		t.Fatalf("targets:\n%s\nwant:\n%s", strings.Join(lines, "\n"), want)
	}
}

func TestTargetsKeepTheOrganizationsWhenMembershipsCannotBeRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/memberships/orgs" {
			http.Error(w, `{"message":"Resource not accessible by personal access token"}`, http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `[{"full_name":"acme/api","name":"api","html_url":"https://github.com/acme/api","owner":{"login":"acme","type":"Organization"},"permissions":{"admin":true}}]`)
	}))
	defer srv.Close()
	got, err := (&REST{BaseURL: srv.URL}).Targets(context.Background(), "tok")
	if err != nil || len(got.Targets) != 2 || got.Targets[0].Kind != "organization" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestTargetsSayWhenTheListIsCut(t *testing.T) {
	var srv *httptest.Server
	pages := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			fmt.Fprint(w, `[]`)
			return
		}
		pages++
		w.Header().Set("Link", fmt.Sprintf(`<%s/user/repos?page=%d>; rel="next"`, srv.URL, pages+1))
		fmt.Fprintf(w, `[{"full_name":"u/r%d","name":"r%d","html_url":"https://github.com/u/r%d","owner":{"login":"u","type":"User"},"permissions":{"admin":true}}]`, pages, pages, pages)
	}))
	defer srv.Close()
	got, err := (&REST{BaseURL: srv.URL}).Targets(context.Background(), "tok")
	if err != nil || !got.Truncated || pages != maxTargetPages || len(got.Targets) != maxTargetPages {
		t.Fatalf("truncated=%v pages=%d targets=%d err=%v", got.Truncated, pages, len(got.Targets), err)
	}
}

func TestTargetsDoNotSendTheTokenToAnotherHost(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the token went to another host: %q", r.Header.Get("Authorization"))
	}))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/repos" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/user/repos?page=2>; rel="next"`, elsewhere.URL))
		}
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	if _, err := (&REST{BaseURL: srv.URL}).Targets(context.Background(), "tok"); err == nil {
		t.Fatal("a next page on another host should fail the listing")
	}
}
