// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/soma-intelligence/open-health-sync/connectors/all"
	"github.com/soma-intelligence/open-health-sync/internal/audit"
	"github.com/soma-intelligence/open-health-sync/internal/auth"
	"github.com/soma-intelligence/open-health-sync/internal/config"
	"github.com/soma-intelligence/open-health-sync/internal/connector"
	"github.com/soma-intelligence/open-health-sync/internal/model"
	"github.com/soma-intelligence/open-health-sync/internal/providerset"
	"github.com/soma-intelligence/open-health-sync/internal/tenant"
)

func TestPathTenantID(t *testing.T) {
	id := uuid.New()
	r := httptest.NewRequest("GET", "/v1/admin/tenants/"+id.String(), nil)
	r.SetPathValue("tenant_id", id.String())
	w := httptest.NewRecorder()
	got, ok := pathTenantID(w, r)
	require.True(t, ok)
	assert.Equal(t, id, got)

	bad := httptest.NewRequest("GET", "/v1/admin/tenants/nope", nil)
	bad.SetPathValue("tenant_id", "nope")
	w = httptest.NewRecorder()
	got, ok = pathTenantID(w, bad)
	require.False(t, ok)
	assert.Equal(t, uuid.Nil, got)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "tenant not found", readProblem(t, w).Detail)
}

func TestClientIDHint(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want string
	}{
		{"an empty id has no hint", "", ""},
		{"a short id keeps its first characters", "abc", "ab..."},
		{"twelve characters is still short", "abcdefghijkl", "ab..."},
		{"thirteen characters keep both ends", "abcdefghijklm", "abcd...jklm"},
		{"a long id keeps both ends", "verylongclientidvalue", "very...alue"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clientIDHint(tc.id)
			if tc.want == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.want, *got)
		})
	}
}

type stubConnector struct{}

func (stubConnector) Slug() string                  { return "withings" }
func (stubConnector) Enabled(connector.Config) bool { return true }
func (stubConnector) Map(connector.RawRecord) (model.MapResult, error) {
	return model.MapResult{}, nil
}

type signInStub struct{ connector.Connector }

func (signInStub) SignIn(context.Context, string, string) (connector.Tokens, error) {
	return connector.Tokens{}, nil
}

func TestIsPassword(t *testing.T) {
	assert.False(t, isPassword(stubConnector{}), "a connector without SignIn is not a password connector")
	assert.True(t, isPassword(signInStub{stubConnector{}}))
}

func TestCheckAdminTenantName(t *testing.T) {
	cases := []struct {
		name string
		give string
		want string
	}{
		{"an empty name is required", "", "name is required"},
		{"the longest allowed name passes", strings.Repeat("a", maxAdminTenantName), ""},
		{"one character over is refused", strings.Repeat("a", maxAdminTenantName+1), "name is at most 100 characters"},
		{"a control character is refused", "two\nlines", "name must not contain control characters"},
		{"an ordinary name passes", "Acme Health", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, checkAdminTenantName(tc.give))
		})
	}
}

func TestToAdminTenantJSON(t *testing.T) {
	created := time.Date(2026, 3, 1, 9, 30, 0, 0, time.FixedZone("UTC+2", 2*3600))
	got := toAdminTenantJSON(tenant.TenantSummary{
		Tenant:     tenant.Tenant{ID: uuid.MustParse("6f9619ff-8b86-4d01-b42d-00cf4fc964ff"), Name: "Acme", CreatedAt: created, Admin: true},
		ActiveKeys: 3,
	})
	assert.Equal(t, "6f9619ff-8b86-4d01-b42d-00cf4fc964ff", got.ID.String())
	assert.Equal(t, "Acme", got.Name)
	assert.True(t, got.Admin)
	assert.Equal(t, 3, got.ActiveKeys)
	assert.Equal(t, "UTC", got.CreatedAt.Location().String())
	assert.True(t, created.Equal(got.CreatedAt), "the instant is kept")
}

func TestAdminEvent(t *testing.T) {
	r := httptest.NewRequest("PATCH", "/v1/admin/tenants/x/keys", nil)
	r.Pattern = "PATCH /v1/admin/tenants/{tenant_id}/keys"
	p := auth.Principal{TenantID: uuid.New(), KeyID: "key_9f2", Admin: true}

	ev := adminEvent(r, p, "tenant_key.update", http.StatusNoContent, map[string]any{"provider": "withings"})
	assert.Equal(t, audit.Admin, ev.ActorType)
	assert.Equal(t, "api_key:key_9f2", ev.ActorID)
	assert.Equal(t, "tenant_key.update", ev.Action)
	assert.Equal(t, "PATCH", ev.Method)
	assert.Equal(t, "PATCH /v1/admin/tenants/{tenant_id}/keys", ev.Route)
	assert.Equal(t, http.StatusNoContent, ev.Status)
	assert.Equal(t, p.TenantID.String(), ev.Detail["admin_tenant_id"])
	assert.Equal(t, "withings", ev.Detail["provider"])
}

func TestProviderErr(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		pass   bool
		status int
		detail string
	}{
		{"a nil error passes", nil, true, 0, ""},
		{"an unknown provider is a 404", fmt.Errorf("update: %w", providerset.ErrUnknown), false, http.StatusNotFound, "provider not found"},
		{"a built-in provider is a 409", providerset.ErrBuiltIn, false, http.StatusConflict, "needs no key and is always on"},
		{"environment keys are a 409", providerset.ErrEnvKeys, false, http.StatusConflict, "come from its OHS_ environment variables"},
		{"a field error is a 400", &providerset.FieldError{Msg: "CLIENT_ID is empty"}, false, http.StatusBadRequest, "CLIENT_ID is empty"},
		{"a refusal is a 422", &providerset.RefusedError{Msg: "the connector refused the keys"}, false, http.StatusUnprocessableEntity, "the connector refused the keys"},
		{"anything else is a 500", errors.New("pool closed"), false, http.StatusInternalServerError, "an internal error occurred"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("PATCH", "/v1/admin/providers/withings", nil)
			r.Pattern = "PATCH /v1/admin/providers/{provider}"
			ok := providerErr(w, r, tc.err)
			assert.Equal(t, tc.pass, ok)
			if tc.pass {
				assert.Equal(t, 0, w.Body.Len(), "a passing error writes nothing")
				return
			}
			assert.Equal(t, tc.status, w.Code)
			p := readProblem(t, w)
			assert.Contains(t, p.Detail, tc.detail)
			if tc.status == http.StatusInternalServerError {
				assert.NotContains(t, w.Body.String(), "pool closed", "the cause is not sent")
			}
		})
	}
}

func TestToAdminProvider(t *testing.T) {
	c, ok := connector.Get("withings")
	require.True(t, ok, "withings is registered")
	srv := &server{d: Deps{PublicURL: "https://ohs.example.test/"}}
	updated := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

	got := srv.toAdminProvider(providerset.State{
		Slug:      "withings",
		Connector: c,
		Status:    providerset.StatusActive,
		Source:    "console",
		Keys:      config.ProviderKeys{"CLIENT_ID": "public", "CLIENT_SECRET": "hidden"},
		Required:  []string{"CLIENT_ID", "CLIENT_SECRET"},
		Optional:  []string{"REGION", "CLIENT_ID"},
		Enabled:   true,
		UpdatedBy: "key_1a2",
		UpdatedAt: &updated,
	})

	assert.Equal(t, "withings", got.Slug)
	assert.Equal(t, "Withings", got.Name)
	assert.Equal(t, []string{"oauth", "push"}, got.Kinds, "withings pulls and sends notifications")
	assert.Equal(t, providerset.StatusActive, got.Status)
	assert.True(t, got.Enabled)
	require.NotNil(t, got.Source)
	assert.Equal(t, "console", *got.Source)
	assert.Nil(t, got.Error)
	require.NotNil(t, got.UpdatedAt)
	assert.True(t, updated.Equal(*got.UpdatedAt))
	require.NotNil(t, got.UpdatedBy)
	assert.Equal(t, "key_1a2", *got.UpdatedBy)

	var names, envs []string
	var required, set []bool
	for _, k := range got.Keys {
		names = append(names, k.Name)
		envs = append(envs, k.Env)
		required = append(required, k.Required)
		set = append(set, k.Set)
	}
	assert.Equal(t, []string{"CLIENT_ID", "CLIENT_SECRET", "REGION"}, names, "the optional key repeated in required is kept once")
	assert.Equal(t, []string{"OHS_WITHINGS_CLIENT_ID", "OHS_WITHINGS_CLIENT_SECRET", "OHS_WITHINGS_REGION"}, envs)
	assert.Equal(t, []bool{true, true, false}, required)
	assert.Equal(t, []bool{true, true, false}, set)

	require.NotNil(t, got.URLs.Redirect, "an OAuth provider has a redirect URI")
	assert.Equal(t, "https://ohs.example.test/oauth/withings/callback", *got.URLs.Redirect)
	require.NotNil(t, got.URLs.Inbound, "a push provider has an inbound URL")
	assert.Equal(t, "https://ohs.example.test/inbound/withings", *got.URLs.Inbound)

	withErr := srv.toAdminProvider(providerset.State{Slug: "x", Connector: c, Status: providerset.StatusOff, Error: "pool closed"})
	require.NotNil(t, withErr.Error)
	assert.Equal(t, "pool closed", *withErr.Error)
	assert.Nil(t, withErr.UpdatedAt)
	assert.Nil(t, withErr.UpdatedBy)
}
