package main

import "net/http"

// uiHandler returns the embedded web UI (nil until the UI package exists).
func uiHandler() http.Handler { return nil }
