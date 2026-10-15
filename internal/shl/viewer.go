// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"embed"
	"net/http"
)

//go:embed viewer/view.html viewer/view.mjs viewer/view.css
var viewerFS embed.FS

// ViewerCSP is the Content-Security-Policy of every viewer response: its
// own script and stylesheet only, no inline code, calls to this origin only,
// no framing, no form submission (the script posts the manifest request).
const ViewerCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; " +
	"base-uri 'none'; frame-ancestors 'none'; form-action 'none'"

type viewerAsset struct {
	file, contentType string
}

// viewerAssets are the only files served under /shl/view.
var viewerAssets = map[string]viewerAsset{
	"":         {"viewer/view.html", "text/html; charset=utf-8"},
	"view.mjs": {"viewer/view.mjs", "text/javascript; charset=utf-8"},
	"view.css": {"viewer/view.css", "text/css; charset=utf-8"},
}

// MountViewer registers GET /shl/view and its two assets on mux (HEAD
// follows GET). The viewer has no state and no credential: the link it opens
// is in the address fragment.
func MountViewer(mux *http.ServeMux) {
	mux.HandleFunc("GET /shl/view", func(w http.ResponseWriter, r *http.Request) { serveViewer(w, "") })
	mux.HandleFunc("GET /shl/view/{asset}", func(w http.ResponseWriter, r *http.Request) {
		serveViewer(w, r.PathValue("asset"))
	})
}

func serveViewer(w http.ResponseWriter, name string) {
	h := w.Header()
	h.Set("Content-Security-Policy", ViewerCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-cache")
	a, ok := viewerAssets[name]
	if !ok {
		h.Set("Cache-Control", "no-store")
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}
	body, err := viewerFS.ReadFile(a.file)
	if err != nil { // cannot happen: the files are embedded at build time
		http.Error(w, "viewer asset missing", http.StatusInternalServerError)
		return
	}
	h.Set("Content-Type", a.contentType)
	_, _ = w.Write(body)
}
