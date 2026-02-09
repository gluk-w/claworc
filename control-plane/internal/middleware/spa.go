package middleware

import (
	"io/fs"
	"net/http"
	"strings"
)

type SPAHandler struct {
	fs        http.FileSystem
	indexHTML []byte
}

func NewSPAHandler(fsys fs.FS) *SPAHandler {
	index, _ := fs.ReadFile(fsys, "index.html")
	return &SPAHandler{
		fs:        http.FS(fsys),
		indexHTML: index,
	}
}

func (h *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/health" {
		http.NotFound(w, r)
		return
	}

	// Try to serve the actual file
	path := strings.TrimPrefix(r.URL.Path, "/")
	if f, err := h.fs.Open(path); err == nil {
		defer f.Close()
		if stat, err := f.Stat(); err == nil && !stat.IsDir() {
			http.FileServer(h.fs).ServeHTTP(w, r)
			return
		}
	}

	// Fall back to index.html for client-side routing
	if h.indexHTML != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(h.indexHTML)
		return
	}

	http.NotFound(w, r)
}
