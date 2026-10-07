package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesAssetsAndFallsBackToIndex(t *testing.T) {
	h := newHandler(fstest.MapFS{
		"index.html":          {Data: []byte("<html>app</html>")},
		"assets/app-abc12.js": {Data: []byte("console.log(1)")},
		"favicon.svg":         {Data: []byte("<svg/>")},
	})
	cases := []struct {
		path, want, cache string
		code              int
	}{
		{"/", "app", "no-cache", 200},
		{"/jobs/123", "app", "no-cache", 200},
		{"/assets/app-abc12.js", "console.log", "immutable", 200},
		{"/favicon.svg", "<svg/>", "", 200},
		{"/api/v1/unknown", "", "", 404},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) || !strings.Contains(rec.Header().Get("Cache-Control"), tc.cache) {
			t.Errorf("%s: %d %q cache=%q", tc.path, rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
	}
}

func TestHandlerExplainsWhenTheUIIsNotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler(fstest.MapFS{".keep": {}}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "make web") {
		t.Fatalf("not built: %d %q", rec.Code, rec.Body.String())
	}
}
