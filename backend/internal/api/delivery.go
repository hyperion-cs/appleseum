package api

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/hyperion-cs/appleseum/applekit/hosting"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/sharing"
	"appleseum/backend/internal/wire"
)

// hostingRequest describes a request by the addresses an installation link depends on.
func hostingRequest(r *http.Request) hosting.Request {
	peer, _, _ := net.SplitHostPort(r.RemoteAddr)
	request := hosting.Request{Host: r.Host, ClientIp: net.ParseIP(peer)}
	if local, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); ok && local != nil {
		request.LocalIp = local.IP
	}
	return request
}

// allowed reports whether a Host header names this server. Outside public mode
// that is localhost, a configured host or a private address.
func (a *Server) allowed(host string, manifestAlias bool) bool {
	if name, port, err := net.SplitHostPort(host); err == nil {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false
		}
		host = name
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		if host = host[1 : len(host)-1]; net.ParseIP(host) == nil {
			return false
		}
	}
	host, err := hosting.NormalizeHost(host)
	switch {
	case err != nil:
		return false
	case a.cfg.Public:
		return true
	// iSH DNS aliases exist only for HTTPS manifests, never for the interface, the API or IPAs.
	case !manifestAlias && a.cfg.Install.ManifestOnlyHost(host):
		return false
	case host == "localhost":
		return true
	}
	for _, known := range a.cfg.Install.LocalHosts {
		if host == known {
			return true
		}
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// delivery serves what a device needs to install a share: the manifest and
// the IPAs it lists. Nothing here needs a session; a share id is the secret.
func (a *Server) delivery() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /manifests/{manifest}", a.manifest)
	mux.HandleFunc("GET /packages/{share}/{file}", a.sharedFile)
	return a.debug.Trace("Hosting", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifest := strings.HasPrefix(r.URL.Path, "/manifests/") && strings.HasSuffix(r.URL.Path, ".plist")
		switch {
		case !a.allowed(r.Host, manifest && r.TLS != nil):
			http.NotFound(w, r)
		case r.Method != http.MethodGet && r.Method != http.MethodHead:
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
		case !a.limiter.Allow(Files, addressIdentity(r)):
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-store")
			mux.ServeHTTP(w, r)
		}
	}))
}

// deliveryFailure answers without a body: the client is iOS, not the interface.
func deliveryFailure(w http.ResponseWriter, r *http.Request, err error) {
	if fault.Is(err, fault.NotFound) {
		http.NotFound(w, r)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (a *Server) manifest(w http.ResponseWriter, r *http.Request) {
	id, isManifest := strings.CutSuffix(r.PathValue("manifest"), ".plist")
	if !isManifest || !wire.Uuid(id) {
		http.NotFound(w, r)
		return
	}
	trace, valid := requestTrace(w, r)
	if !valid {
		return
	}
	apps, packages, err := sharing.Manifest(r.Context(), a.db, id)
	// Only a traced GET belongs to an installation attempt; HEAD probes do not.
	finish, delivered := func(bool) {}, false
	if err == nil && trace != "" && r.Method == http.MethodGet {
		finish = a.progress.Manifest(id, trace, packages)
	}
	defer func() { finish(delivered) }()
	for i := range apps {
		apps[i].Path = tracedPath(apps[i].Path, trace)
	}
	var body []byte
	if err == nil {
		body, err = a.cfg.Install.Manifest(hostingRequest(r), apps)
	}
	if err != nil {
		deliveryFailure(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodGet {
		n, err := w.Write(body)
		delivered = err == nil && n == len(body) && r.Context().Err() == nil
	}
}

func (a *Server) sharedFile(w http.ResponseWriter, r *http.Request) {
	share := r.PathValue("share")
	file, isIpa := strings.CutSuffix(r.PathValue("file"), ".ipa")
	if !isIpa || !wire.Uuid(share) || !wire.Uuid(file) {
		http.NotFound(w, r)
		return
	}
	trace, valid := requestTrace(w, r)
	if !valid {
		return
	}
	path, size, err := sharing.File(r.Context(), a.db, share, file)
	if err != nil {
		deliveryFailure(w, r, err)
		return
	}
	// A traced GET counts even when the file then fails to open: the device asked for it.
	if trace != "" && r.Method == http.MethodGet {
		observed := &deliveryProgress{ResponseWriter: w, transfer: a.progress.File(share, trace, file, size)}
		defer observed.finish(r)
		w = observed
	}
	content, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer content.Close()
	info, err := content.Stat()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, file+".ipa", info.ModTime(), content)
}
