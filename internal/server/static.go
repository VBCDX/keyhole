package server

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler serves the embedded web app. Unknown paths get index.html
// so the app's own routes work on reload. Files under /assets/ have content
// hashes in their names, so they can be cached for good; index.html cannot.
func (s *Server) staticHandler() http.Handler {
	if s.web == nil {
		return http.HandlerFunc(notBuilt)
	}
	if _, err := fs.Stat(s.web, "index.html"); err != nil {
		return http.HandlerFunc(notBuilt)
	}
	files := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(s.web, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, s.web, "index.html")
	})
}

func notBuilt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("The Keyhole web app is not built into this binary. Run `npm ci && npm run build` in web/ and rebuild.\n"))
}
