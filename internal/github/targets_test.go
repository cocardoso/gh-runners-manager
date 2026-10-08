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
		if r.URL.Path != "/user/repos" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/user/repos?page=2&per_page=100>; rel="next"`, srv.URL))
			fmt.Fprint(w, `[{"full_name":"cocardoso/zeropaper","name":"zeropaper","private":true,"html_url":"https://github.com/cocardoso/zeropaper","owner":{"login":"cocardoso","type":"User"}},
				{"full_name":"acme/api","name":"api","private":false,"html_url":"https://github.com/acme/api","owner":{"login":"acme","type":"Organization"}}]`)
		case "2":
			fmt.Fprint(w, `[{"full_name":"acme/web","name":"web","private":true,"html_url":"https://github.com/acme/web","owner":{"login":"acme","type":"Organization"}}]`)
		}
	}))
	defer srv.Close()
	got, err := (&REST{BaseURL: srv.URL}).Targets(context.Background(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, g := range got {
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
