// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/api/read"
)

var update = flag.Bool("update", false, "rewrite the viewer's test fixture")

// fixture is what the viewer's node test decrypts: an invented key, the
// payload built with it, and the file the package encrypted.
type fixture struct {
	Key     string `json:"key"`
	URI     string `json:"uri"`
	JWE     string `json:"jwe"`
	Entries int    `json:"entries"`
}

// TestViewerFixture keeps the node test's fixture a file this package wrote
// and can read back, so the Go and the browser side agree on the format.
// go test ./internal/shl -run TestViewerFixture -update rewrites it.
func TestViewerFixture(t *testing.T) {
	path := filepath.Join("viewer", "testdata", "fixture.json")
	key := testKey()
	if *update {
		var buf bytes.Buffer
		d := NewDeflater(&buf)
		snap, err := NewSnapshot(d, SnapshotOptions{User: snapUser, Base: snapBase, At: snapAt,
			Categories: []string{"heart", "blood_pressure", "sleep", "activity"}})
		require.NoError(t, err)
		for _, rec := range []read.ObservationRecord{
			quantity(1, "invented_ring", "heart_rate", 61, "count/min"),
			quantity(2, "invented_ring", "heart_rate", 64, "count/min"),
			panel(3),
			sleepRecord(5, "invented_ring"),
			workoutRecord(6),
			dailyRecord(7),
		} {
			require.NoError(t, snap.Add(rec))
		}
		require.NoError(t, snap.Close())
		require.NoError(t, d.Close())
		jwe, err := EncryptDeflated(key, buf.Bytes(), ContentFHIR)
		require.NoError(t, err)
		uri, err := Payload{URL: "https://ohs.example/shl/" + strings.Repeat("A", 43), Key: EncodeKey(key),
			Label: "Invented share"}.URI()
		require.NoError(t, err)
		out, err := json.MarshalIndent(fixture{Key: EncodeKey(key), URI: uri, JWE: jwe, Entries: 1 + 6 + 2}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(out, '\n'), 0o644))
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var f fixture
	require.NoError(t, json.Unmarshal(raw, &f))
	k, err := DecodeKey(f.Key)
	require.NoError(t, err)
	plain, hdr, err := Decrypt(k, f.JWE, 1<<20)
	require.NoError(t, err)
	assert.Equal(t, ZipDeflate, hdr.Zip)
	var b bundleJSON
	require.NoError(t, json.Unmarshal(plain, &b))
	assert.Equal(t, f.Entries, len(b.Entry), "the Patient, six Observations, two Devices")
	p, err := Parse(f.URI)
	require.NoError(t, err)
	assert.Equal(t, f.Key, p.Key)
}

func TestViewerHandler(t *testing.T) {
	mux := http.NewServeMux()
	MountViewer(mux)
	for path, ct := range map[string]string{
		"/shl/view":          "text/html; charset=utf-8",
		"/shl/view/view.mjs": "text/javascript; charset=utf-8",
		"/shl/view/view.css": "text/css; charset=utf-8",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, path)
		h := rec.Header()
		assert.Equal(t, ct, h.Get("Content-Type"), path)
		assert.Equal(t, ViewerCSP, h.Get("Content-Security-Policy"), path)
		assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"), path)
		assert.Equal(t, "no-referrer", h.Get("Referrer-Policy"), path)
		assert.Equal(t, "DENY", h.Get("X-Frame-Options"), path)
		assert.NotEmpty(t, rec.Body.Bytes(), path)
	}
	for _, path := range []string{"/shl/view/", "/shl/view/../shl.go", "/shl/view/testdata/fixture.json", "/shl/view/x"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.NotEqual(t, http.StatusOK, rec.Code, path)
	}
	assert.Contains(t, ViewerCSP, "script-src 'self'")
	assert.Contains(t, ViewerCSP, "connect-src 'self'")
	assert.NotContains(t, ViewerCSP, "unsafe")

	// The page loads its script and stylesheet by relative path, no inline code.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/shl/view", nil))
	page := rec.Body.String()
	assert.Contains(t, page, `src="view/view.mjs"`, "relative: the server may be published under a path")
	assert.Contains(t, page, `href="view/view.css"`)
	assert.NotContains(t, page, "<script>")
	assert.NotContains(t, page, "style=")
	assert.NotRegexp(t, `\son[a-z]+=`, page, "no inline handler")
}
