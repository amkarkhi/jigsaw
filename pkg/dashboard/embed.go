package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist embeds the built frontend. The directory is populated by `make web`
// (which runs the Vite build). A minimal placeholder is checked in so go:embed
// works on a fresh clone before `make web` has been run.
//
//go:embed all:dist
var dist embed.FS

// staticHandler returns an http.Handler that serves the embedded SPA.
//
// SPA-aware routing:
//   - asset files under /assets/ are served directly from the embedded FS.
//   - anything else (including "/", "/flows", "/tasks/foo") is served the
//     bundled index.html so the client-side router resolves the path.
//
// We do NOT use http.FileServer because its auto-redirect-to-index behaviour
// fights with sub-path routing (it 301s "/" → "./" → "/", which loops).
// Serving index.html bytes ourselves is both simpler and faster.
func staticHandler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "dashboard frontend not embedded (run `make web`)", http.StatusInternalServerError)
		})
	}
	indexBytes, _ := fs.ReadFile(sub, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean != "" {
			data, err := fs.ReadFile(sub, clean)
			if err == nil {
				w.Header().Set("Content-Type", mimeFor(clean))
				_, _ = w.Write(data)
				return
			}
		}
		// SPA fallback.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexBytes)
	})
}

// mimeFor returns a Content-Type guess for the most common asset extensions
// we ship from Vite. Falls back to octet-stream.
func mimeFor(path string) string {
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	}
	return "application/octet-stream"
}
