// Package webui serves the dashboard's static files, which are built from
// web/ into static/dist and embedded in the binary.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:static
var static embed.FS

// Dist returns the embedded dashboard build, or nil when the binary was
// built without it.
func Dist() fs.FS {
	sub, err := fs.Sub(static, "static/dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// Content types are set explicitly: on Windows the system registry can map
// .js to text/plain, which browsers refuse to run as a module.
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".webp":        "image/webp",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

type file struct {
	data        []byte
	etag        string
	contentType string
}

// Handler serves a built single-page app from fsys. Files under assets/
// have content hashes in their names and are cached for a year; everything
// else is revalidated. Unknown paths without a file extension get
// index.html so client-side routes work on reload.
type Handler struct {
	files map[string]file
}

// NewHandler loads every file in fsys into memory. A nil fsys produces a
// handler that explains how to build the dashboard.
func NewHandler(fsys fs.FS) (*Handler, error) {
	h := &Handler{files: map[string]file{}}
	if fsys == nil {
		return h, nil
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		ct := contentTypes[strings.ToLower(path.Ext(p))]
		if ct == "" {
			ct = "application/octet-stream"
		}
		h.files[p] = file{data: data, etag: `"` + hex.EncodeToString(sum[:12]) + `"`, contentType: ct}
		return nil
	})
	return h, err
}

// Built reports whether the dashboard files are available.
func (h *Handler) Built() bool {
	_, ok := h.files["index.html"]
	return ok
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	if !h.Built() {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(notBuiltPage))
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	f, ok := h.files[name]
	if !ok {
		if path.Ext(name) != "" || strings.HasPrefix(name, "assets/") {
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		}
		name, f = "index.html", h.files["index.html"]
	}

	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("Content-Type", f.contentType)
	w.Header().Set("ETag", f.etag)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(f.data))
}

const notBuiltPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Relaybox: dashboard not built</title>
</head>
<body>
<h1>Relaybox is running, but this binary has no dashboard</h1>
<p>The capture endpoints and the API work. The dashboard files are added when you build the web app before the Go binary:</p>
<pre>make build</pre>
<p>or, without make:</p>
<pre>cd web
npm ci
npm run build
cd ..
go build -o bin/relaybox ./cmd/relaybox</pre>
<p>During development, run <code>npm run dev</code> in <code>web/</code> and open <a href="http://localhost:5173">http://localhost:5173</a>.</p>
<p>Health check: <a href="/healthz">/healthz</a>. API description: <a href="/api/v1/openapi.yaml">/api/v1/openapi.yaml</a>.</p>
</body>
</html>
`
