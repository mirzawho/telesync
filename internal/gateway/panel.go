package gateway

import (
	"fmt"
	"net/http"
	"strings"
)

// The admin panel, served from the same listener and the same handler as the
// API.
//
// The panel is a single-page app: its routes (/admin/login, /admin/channels,
// …) exist only in the browser, so a browser asking for a document gets the
// app's one page and its router takes over. Anything else under the mount is a
// part of the app — a script, a stylesheet, a fetch — and reaches only what the
// build produced, so a broken asset stays a broken asset.
//
// The panel is a page, not data: it is served without a credential. Every API
// route it calls checks its caller.

// servePanel answers a request that belongs to the panel and reports whether it
// did. Without a mount there is no panel: its addresses are the pages and the
// API at the root.
func (s *Server) servePanel(w http.ResponseWriter, r *http.Request, requestID string) bool {
	base := s.cfg.BasePath
	if base == "" || !underPath(r.URL.Path, base) {
		return false
	}
	// The API's paths under the mount are its own, whatever the method or the
	// Accept header: a POST to the login route is not a panel page.
	if underPath(r.URL.Path, s.fullPath("/api")) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.fail(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			"The panel is read with GET.", requestID)
		return true
	}
	if s.cfg.Panel == nil {
		s.panelMissing(w, r, requestID)
		return true
	}
	if isNavigation(r) {
		s.panelIndex(w, r)
		return true
	}
	s.cfg.Panel.ServeHTTP(w, r)
	return true
}

// isNavigation reports whether a request is a browser following a link or
// loading an address, rather than one of the app's files or a fetch.
func isNavigation(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	if r.Header.Get("Sec-Fetch-Dest") != "" {
		return false
	}
	// A caller that sends no fetch metadata at all (curl, an older browser)
	// asks for a page by its Accept header.
	accept := r.Header.Get("Accept")
	return accept == "" || strings.Contains(accept, "text/html")
}

// panelMissing answers a request to a panel route in a binary built without the
// panel: a browser asking for a page is told how to get one, and anything else
// finds nothing.
func (s *Server) panelMissing(w http.ResponseWriter, r *http.Request, requestID string) {
	if !isNavigation(r) {
		s.notFound(w, requestID)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintln(w, "This binary was built without the admin panel. Run `make build` "+
		"(it builds the panel and folds it in), then restart.")
}

// panelIndex serves the panel's single page for a client-side route.
//
// The file server is reached through the mount root rather than /index.html:
// it answers that name with a redirect to the directory, which is right for a
// browser's own URL and wrong for the page the gateway asks for itself.
// A request without a credential that asks for a file is a file: it gets
// whichever one its path names, and 404 when there is none.
func (s *Server) panelIndex(w http.ResponseWriter, r *http.Request) {
	forwarded := r.Clone(r.Context())
	forwarded.URL.Path = s.cfg.BasePath + "/"
	forwarded.URL.RawPath = ""
	s.cfg.Panel.ServeHTTP(w, forwarded)
}
