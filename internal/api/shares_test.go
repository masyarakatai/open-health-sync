// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/shl"
	"github.com/soma-intelligence/open-health-sync/internal/tenant"
)

// The public share routes answer what they can before any query: a
// malformed secret, a preflight, a HEAD, the viewer.
func TestShareRoutesOffline(t *testing.T) {
	h := offlineRouter(t)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	rec := do("POST", "/shl/not-a-secret", `{"recipient":"x"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Equal(t, http.StatusNotFound, do("POST", "/shl/view", `{"recipient":"x"}`).Code, "view is no manifest id")
	assert.Equal(t, http.StatusNotFound, do("GET", "/shl/file/not-a-secret", "").Code)

	rec = do("OPTIONS", "/shl/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "POST", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))

	rec = do("HEAD", "/shl/file/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, "a HEAD never spends a ticket")
	assert.Equal(t, "GET", rec.Header().Get("Allow"))

	rec = do("GET", "/shl/view", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, shl.ViewerCSP, rec.Header().Get("Content-Security-Policy"))
	assert.NotEmpty(t, rec.Header().Get("X-Request-Id"), "through the instrumented router")
	assert.Equal(t, http.StatusOK, do("GET", "/shl/view/view.mjs", "").Code)
	assert.Equal(t, http.StatusNotFound, do("GET", "/shl/view/missing.js", "").Code)

	// The management routes need a tenant key.
	assert.Equal(t, http.StatusUnauthorized,
		do("POST", "/v1/users/00000000-0000-4000-8000-000000000001/shares", `{}`).Code)
}

// The manifest and file routes share one bucket per client address, on
// OHS_RATE_LIMIT_OAUTH, counted as route_group="share".
func TestShareRateLimit(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	h := NewRouter(Deps{Pool: pool, Store: tenant.New(pool), RateLimitOAuth: RateLimit{Rate: 0.001, Burst: 2},
		Metrics: true, MetricsToken: "invented-metrics-token"})
	do := func(method, path, remote string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(`{"recipient":"x"}`))
		r.RemoteAddr = remote
		h.ServeHTTP(rec, r)
		return rec
	}
	assert.Equal(t, http.StatusNotFound, do("POST", "/shl/x", "192.0.2.7:1").Code)
	assert.Equal(t, http.StatusNotFound, do("GET", "/shl/file/x", "192.0.2.7:1").Code)
	rec := do("POST", "/shl/x", "192.0.2.7:1")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "the third request from one address within the burst")
	assert.Equal(t, "1000", rec.Header().Get("Retry-After"))
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"), "a viewer on another origin reads the 429")
	assert.Contains(t, rec.Header().Get("Access-Control-Expose-Headers"), "Retry-After")
	assert.Equal(t, http.StatusNotFound, do("POST", "/shl/x", "192.0.2.8:1").Code, "another address")
	assert.Equal(t, http.StatusOK, do("GET", "/shl/view", "192.0.2.7:1").Code, "the viewer is not limited")

	mrec := httptest.NewRecorder()
	mreq := httptest.NewRequest("GET", "/metrics", nil)
	mreq.Header.Set("Authorization", "Bearer invented-metrics-token")
	h.ServeHTTP(mrec, mreq)
	require.Equal(t, http.StatusOK, mrec.Code)
	assert.Contains(t, mrec.Body.String(), `ohs_rate_limited_total{route_group="share"} 1`)
}
