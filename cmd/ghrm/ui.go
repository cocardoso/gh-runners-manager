package main

import (
	"net/http"

	"github.com/cocardoso/gh-runners-manager/web"
)

// uiHandler returns the embedded web UI.
func uiHandler() http.Handler { return web.Handler() }
