package apidocs_test

import (
	"io"
	"net/http"
	"strings"
)

// ScriptedSite returns a WordPress REST API handler for tests that drive the
// real client without a server: settings from settings, attachments from
// mediaByID, and a REST 404 for anything else.
//
// It lives in this file rather than in internal/wordpress so that only tests
// import it: an exported TestSite there would read as a test to `go vet`.
func ScriptedSite(settings string, mediaByID map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		switch {
		case strings.HasSuffix(r.URL.Path, "/wp-json/wp/v2/settings"):
			_, _ = io.WriteString(w, settings)
		// A publishing form reads the destination's own taxonomy, so the
		// scripted site answers those two listings as a real site would.
		case strings.HasSuffix(r.URL.Path, "/wp-json/wp/v2/categories"),
			strings.HasSuffix(r.URL.Path, "/wp-json/wp/v2/tags"):
			_, _ = io.WriteString(w, `[{"id":4,"name":"Example","slug":"example","count":1}]`)
		case strings.Contains(r.URL.Path, "/wp-json/wp/v2/media/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			item, ok := mediaByID[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"code":"rest_post_invalid_id"}`)
				return
			}
			_, _ = io.WriteString(w, item)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"code":"rest_no_route"}`)
		}
	}
}
