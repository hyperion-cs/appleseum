package api

import (
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"appleseum/backend/internal/fault"
)

// static serves the built interface: hashed assets forever, pages revalidated,
// and the precompressed copy the browser accepts best.
func (a *Server) static() http.Handler {
	files := http.FileServer(http.FS(a.cfg.Web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			a.fail(w, errMethod)
			return
		}
		if !a.limiter.Allow(Read, addressIdentity(r)) {
			a.fail(w, fault.RateLimited)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if !fs.ValidPath(path) {
			http.NotFound(w, r)
			return
		}
		header := w.Header()
		if strings.HasPrefix(path, "assets/") {
			header.Set("Cache-Control", "public, max-age="+yearSeconds+", immutable")
		} else {
			header.Set("Cache-Control", "no-cache")
		}
		header.Set("Vary", "Accept-Encoding")
		// Route pages are files without an extension.
		contentType := "text/html; charset=utf-8"
		if extension := filepath.Ext(path); extension != "" {
			contentType = mime.TypeByExtension(extension)
		}
		if contentType != "" {
			header.Set("Content-Type", contentType)
		}
		accepted := strings.Join(r.Header.Values("Accept-Encoding"), ",")
		var encoding, suffix string
		var best float64
		for _, choice := range []struct{ name, suffix string }{{"br", ".br"}, {"gzip", ".gz"}} {
			if quality := encodingQuality(accepted, choice.name); quality > best {
				if _, err := fs.Stat(a.cfg.Web, path+choice.suffix); err == nil {
					best, encoding, suffix = quality, choice.name, choice.suffix
				}
			}
		}
		if encoding != "" {
			header.Set("Content-Encoding", encoding)
			compressed := r.Clone(r.Context())
			compressed.URL.Path = "/" + path + suffix
			r = compressed
		}
		files.ServeHTTP(w, r)
	})
}

// encodingQuality is the q-value of an encoding in Accept-Encoding. A named
// encoding overrides the wildcard, including an explicit q=0.
func encodingQuality(header, name string) float64 {
	var wildcard float64
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		encoding := strings.ToLower(strings.TrimSpace(fields[0]))
		if encoding != name && encoding != "*" {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(key, "q") {
				if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed >= 0 && parsed <= 1 {
					quality = parsed
				} else {
					quality = 0
				}
			}
		}
		if encoding == name {
			return quality
		}
		wildcard = quality
	}
	return wildcard
}
