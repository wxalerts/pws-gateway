package web

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const placeholderHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>pwsgw</title>
<meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body><h1>pwsgw web UI is not built</h1>
<p>This binary was built without the web UI bundle. Build it with
<code>cd web &amp;&amp; npm ci &amp;&amp; npm run build</code> and rebuild the daemon.
See <a href="https://github.com/wxalerts/pws-gateway/blob/main/CONTRIBUTING.md">CONTRIBUTING.md</a>.</p>
<p>The JSON API under /api is available.</p></body></html>
`

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotFound, "not_found", "no route for "+r.Method+" "+r.URL.Path)
}

func (s *Server) handleNotImplemented(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusNotImplemented, "not_implemented", "")
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	if s.opts.Dist == nil {
		s.placeholder(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.opts.Dist, name)
	if err != nil {
		if name != "index.html" && (strings.HasPrefix(name, "assets/") || !isRoute(name)) {
			http.NotFound(w, r)
			return
		}
		// History API fallback: unknown paths belong to the client router.
		name = "index.html"
		data, err = fs.ReadFile(s.opts.Dist, name)
		if err != nil {
			s.placeholder(w, r)
			return
		}
	}
	switch {
	case name == "index.html":
		w.Header().Set("Cache-Control", "no-store")
	case strings.HasPrefix(name, "assets/"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// isRoute reports whether a missing path with a dot is still a client route
// (for example a station key such as /stations/Acurite-5n1.12345).
func isRoute(name string) bool {
	return !strings.Contains(path.Base(name), ".") || strings.HasPrefix(name, "stations/")
}

func (s *Server) placeholder(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write([]byte(placeholderHTML)); err != nil {
		s.log.Debug("web: write placeholder", "err", err)
	}
}
