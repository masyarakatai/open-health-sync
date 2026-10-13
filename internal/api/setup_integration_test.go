// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/api"
	"github.com/soma-intelligence/open-health-sync/internal/db"
	"github.com/soma-intelligence/open-health-sync/internal/tenant"
)

const testSetupToken = "setup-token-0123456789"

// setupRouter is a router with the first-run setup on, over a database with
// no tenant.
func setupRouter(t *testing.T) (http.Handler, *tenant.Store) {
	t.Helper()
	pool, err := db.Open(context.Background(), db.TestDB(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	st := tenant.New(pool)
	return api.NewRouter(api.Deps{Pool: pool, Store: st, SetupToken: testSetupToken}), st
}

func setupCall(h http.Handler, method, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/v1/setup", strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func setupNeeded(t *testing.T, h http.Handler) bool {
	t.Helper()
	rec := setupCall(h, "GET", "", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var out struct {
		Needed *bool `json:"needed"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.NotNil(t, out.Needed)
	return *out.Needed
}

func TestSetupFirstRun(t *testing.T) {
	h, st := setupRouter(t)
	assert.True(t, setupNeeded(t, h))

	rec := setupCall(h, "POST", `{"setup_token":"wrong-token-0123456789"}`, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	rec = setupCall(h, "POST", `{}`, "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "no token")
	for _, body := range []string{`{"setup_token":""}`, `{"setup_token":"` + testSetupToken[:len(testSetupToken)-1] + `"}`} {
		assert.Equal(t, http.StatusUnauthorized, setupCall(h, "POST", body, "").Code, body)
	}
	rec = setupCall(h, "POST", `{"setup_token":"`+testSetupToken+`","tenant_name":"a\u0007b"}`, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "control character in the name")
	rec = setupCall(h, "POST", `{"setup_token":"`+testSetupToken+`","tenant_name":"`+strings.Repeat("x", 101)+`"}`, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code, "name too long")
	assert.True(t, setupNeeded(t, h), "refused calls create nothing")

	rec = setupCall(h, "POST", `{"setup_token":"`+testSetupToken+`","tenant_name":"  Home  "}`, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var out struct {
		TenantID string `json:"tenant_id"`
		APIKey   string `json:"api_key"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Regexp(t, `^ohs_sk_`, out.APIKey)
	ts, err := st.ListTenants(context.Background())
	require.NoError(t, err)
	require.Len(t, ts, 1)
	assert.Equal(t, "Home", ts[0].Name)
	assert.Equal(t, out.TenantID, ts[0].ID.String())

	// The key works, and the creation is in the tenant's audit log.
	req := httptest.NewRequest("GET", "/v1/audit?action=tenant.create", nil)
	req.Header.Set("Authorization", "Bearer "+out.APIKey)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var events struct {
		Data []struct {
			Actor  struct{ Type, ID string }
			Route  string
			Status int
			Detail map[string]any
		}
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &events))
	require.Len(t, events.Data, 1)
	ev := events.Data[0]
	assert.Equal(t, "system", ev.Actor.Type)
	assert.Equal(t, "setup", ev.Actor.ID)
	assert.Equal(t, "POST /v1/setup", ev.Route)
	assert.Equal(t, http.StatusCreated, ev.Status)
	assert.Equal(t, out.APIKey[:12]+"...", ev.Detail["key_prefix"])
	assert.NotContains(t, rec.Body.String(), out.APIKey)
	assert.NotContains(t, rec.Body.String(), testSetupToken)

	// Closed for good: the right token, a wrong one, a fresh router.
	assert.False(t, setupNeeded(t, h))
	assert.Equal(t, http.StatusConflict, setupCall(h, "POST", `{"setup_token":"`+testSetupToken+`"}`, "").Code)
	assert.Equal(t, http.StatusConflict, setupCall(h, "POST", `{"setup_token":"nope"}`, "").Code)
	h2 := api.NewRouter(api.Deps{Pool: st.Pool(), Store: st, SetupToken: testSetupToken})
	assert.False(t, setupNeeded(t, h2))
	assert.Equal(t, http.StatusConflict, setupCall(h2, "POST", `{"setup_token":"`+testSetupToken+`"}`, "").Code)
}

func TestSetupClosedWhenATenantExists(t *testing.T) {
	h, st := setupRouter(t)
	_, _, err := st.CreateTenant(context.Background(), "from-cli")
	require.NoError(t, err)
	assert.False(t, setupNeeded(t, h))
	assert.Equal(t, http.StatusConflict, setupCall(h, "POST", `{"setup_token":"`+testSetupToken+`"}`, "").Code)

	// A fresh process (a restart) answers 409 to its very first call, a
	// wrong token included, so the closed route never checks the token.
	h2 := api.NewRouter(api.Deps{Pool: st.Pool(), Store: st, SetupToken: testSetupToken})
	assert.Equal(t, http.StatusConflict, setupCall(h2, "POST", `{"setup_token":"nope"}`, "").Code)
}

func TestSetupConcurrentCalls(t *testing.T) {
	h, st := setupRouter(t)
	const callers = 6
	codes := make([]int, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = setupCall(h, "POST", `{"setup_token":"`+testSetupToken+`"}`, "").Code
		}()
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		if c == http.StatusCreated {
			created++
		} else {
			assert.Equal(t, http.StatusConflict, c)
		}
	}
	assert.Equal(t, 1, created)
	ts, err := st.ListTenants(context.Background())
	require.NoError(t, err)
	require.Len(t, ts, 1)
	assert.Equal(t, "default", ts[0].Name)
}
