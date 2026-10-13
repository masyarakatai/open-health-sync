// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package tenant_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/audit"
	"github.com/soma-intelligence/open-health-sync/internal/auth"
	"github.com/soma-intelligence/open-health-sync/internal/tenant"
)

func TestCreateFirstTenant(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	has, err := s.HasTenants(ctx)
	require.NoError(t, err)
	assert.False(t, has)

	ev := &audit.Event{ActorType: audit.System, ActorID: "setup", Action: audit.TenantCreate,
		Method: "POST", Route: "POST /v1/setup", Status: 201, RequestID: "req-1"}
	ten, key, err := s.CreateFirstTenant(ctx, "home", ev)
	require.NoError(t, err)
	require.Regexp(t, `^ohs_sk_`, key)
	assert.Equal(t, "home", ten.Name)
	p, err := s.LookupKey(ctx, auth.Hash(key))
	require.NoError(t, err)
	assert.Equal(t, ten.ID, p.TenantID)

	// Recorded with the tenant, naming the key by its prefix only.
	var actor, detail string
	require.NoError(t, s.Pool().QueryRow(ctx,
		`SELECT actor_type || ':' || actor_id, detail->>'key_prefix' FROM ohs_core.audit_events
		 WHERE tenant_id = $1 AND action = 'tenant.create'`, ten.ID).Scan(&actor, &detail))
	assert.Equal(t, "system:setup", actor)
	assert.Equal(t, key[:12]+"...", detail)

	has, err = s.HasTenants(ctx)
	require.NoError(t, err)
	assert.True(t, has)

	// Closed for good once a tenant exists.
	_, key2, err := s.CreateFirstTenant(ctx, "again", nil)
	require.ErrorIs(t, err, tenant.ErrSetupDone)
	assert.Empty(t, key2)
	var n int
	require.NoError(t, s.Pool().QueryRow(ctx, `SELECT count(*) FROM ohs_core.tenants`).Scan(&n))
	assert.Equal(t, 1, n)
}

func TestCreateFirstTenantClosedByAnotherPath(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	_, _, err := s.CreateTenant(ctx, "from-cli")
	require.NoError(t, err)
	_, _, err = s.CreateFirstTenant(ctx, "home", nil)
	require.ErrorIs(t, err, tenant.ErrSetupDone)
}

func TestCreateFirstTenantConcurrent(t *testing.T) {
	ctx := context.Background()

	// Two first runs at once, then a crowd: exactly one tenant and one key.
	for _, callers := range []int{2, 8} {
		s := newStore(t)
		var wg sync.WaitGroup
		keys := make([]string, callers)
		errs := make([]error, callers)
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, keys[i], errs[i] = s.CreateFirstTenant(ctx, "home", nil)
			}()
		}
		wg.Wait()
		created := 0
		for i := range callers {
			if errs[i] == nil {
				created++
				assert.Regexp(t, `^ohs_sk_`, keys[i])
			} else {
				assert.ErrorIs(t, errs[i], tenant.ErrSetupDone)
				assert.Empty(t, keys[i])
			}
		}
		assert.Equal(t, 1, created, "%d callers", callers)
		var n int
		require.NoError(t, s.Pool().QueryRow(ctx, `SELECT count(*) FROM ohs_core.tenants`).Scan(&n))
		assert.Equal(t, 1, n, "%d callers", callers)
	}
}
