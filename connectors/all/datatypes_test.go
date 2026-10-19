// SPDX-License-Identifier: AGPL-3.0-only

package all_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/soma-intelligence/open-health-sync/connectors/all"
	"github.com/soma-intelligence/open-health-sync/internal/connector"
)

// recordKinds is every connector.Kind constant: the vocabulary a data_types
// list draws its record kinds from. Quantity names come from internal/model's
// canonical quantity_type vocabulary and are looser by design (a metric
// without a dedicated table passes through with no schema change).
var recordKinds = []string{
	connector.KindSample,
	connector.KindCategory,
	connector.KindSleep,
	connector.KindWorkout,
	connector.KindRoute,
	connector.KindNutrition,
	connector.KindECG,
	connector.KindHeartbeat,
	connector.KindDaily,
	connector.KindObservation,
}

// TestEveryConnectorDeclaresDataTypes fails when a registered connector
// cannot say what it delivers: GET /v1/providers exposes data_types from
// this method, and a connector that omits it would answer with an empty
// list. It also fails on a typo in a kind, a duplicate entry, or an entry
// that is not snake_case.
func TestEveryConnectorDeclaresDataTypes(t *testing.T) {
	all := connector.All()
	require.NotEmpty(t, all)
	for _, c := range all {
		dt, ok := connector.AsDataTypes(c)
		require.True(t, ok, "%s: a registered connector implements connector.DataTypes", c.Slug())
		list := dt.DataTypes()
		require.NotEmpty(t, list, "%s: declares what it delivers", c.Slug())
		seen := map[string]bool{}
		hasKind := false
		for _, v := range list {
			require.False(t, seen[v], "%s: data_types entry %q is not repeated", c.Slug(), v)
			seen[v] = true
			require.Regexp(t, `^[a-z][a-z0-9_]*$`, v, "%s: data_types entry", c.Slug())
			if slices.Contains(recordKinds, v) {
				hasKind = true
			}
		}
		require.True(t, hasKind, "%s: names at least one record kind", c.Slug())
	}
}
