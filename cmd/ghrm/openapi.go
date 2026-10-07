package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/cocardoso/gh-runners-manager/internal/api"
)

// openapi prints the API's OpenAPI document (used to generate the UI's client).
func openapi(stdout, stderr io.Writer) int {
	rec := httptest.NewRecorder()
	api.New(api.Deps{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	if rec.Code != http.StatusOK {
		fmt.Fprintf(stderr, "openapi: %d %s\n", rec.Code, rec.Body.String())
		return 1
	}
	_, _ = stdout.Write(rec.Body.Bytes())
	return 0
}
