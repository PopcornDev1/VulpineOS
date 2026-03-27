package remote

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

// ServePanel adds the web panel static files to an HTTP mux.
// The panel files are embedded in the binary at build time from web/dist/.
// If webFS is nil, the panel is not available and requests get a 404.
func ServePanel(mux *http.ServeMux, webFS fs.FS) {
	if webFS == nil {
		log.Println("web panel not available (no embedded files)")
		return
	}

	fileServer := http.FileServer(http.FS(webFS))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Skip WebSocket and API paths
		if r.URL.Path == "/ws" || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/api/") {
			return
		}

		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Check if file exists in the embedded FS
		if f, err := webFS.Open(strings.TrimPrefix(path, "/")); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	log.Println("web panel serving at /")
}

// EmbedFS is a placeholder for the embedded web panel files.
// The actual embedding happens in cmd/vulpineos/main.go with //go:embed.
var _ embed.FS // ensure embed is importable
