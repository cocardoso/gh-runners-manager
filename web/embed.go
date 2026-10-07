// Package web embeds the built web UI (web/dist) and serves it with an SPA fallback.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded UI.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return newHandler(sub)
}

const notBuilt = `<!doctype html><meta charset="utf-8"><title>gh-runners-manager</title>
<body style="font-family:system-ui;max-width:40rem;margin:4rem auto;line-height:1.5">
<h1>The web UI is not built</h1>
<p>This binary was built without the UI. Run <code>make web</code> and rebuild, or use the API under <a href="/api/docs">/api/docs</a>.</p>`

func newHandler(fsys fs.FS) http.Handler {
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(notBuilt))
		})
	}
	files := http.FileServerFS(fsys)
	index, _ := fs.ReadFile(fsys, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if info, err := fs.Stat(fsys, p); err == nil && !info.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// A missing hashed asset (a stale tab after a deploy) is a 404, not the HTML shell.
		if strings.HasPrefix(p, "assets/") {
			http.NotFound(w, r)
			return
		}
		// Client-side routes get the app shell.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
