// SPDX-License-Identifier: AGPL-3.0-only

package collect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
)

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		expiry time.Time
		want   bool
	}{
		{"a zero expiry never refreshes", time.Time{}, false},
		{"inside the margin refreshes", now.Add(RefreshMargin / 2), true},
		{"an expiry already past refreshes", now.Add(-time.Minute), true},
		{"outside the margin waits", now.Add(RefreshMargin + time.Second), false},
		{"the margin itself waits", now.Add(RefreshMargin), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := needsRefresh(connector.Tokens{Expiry: tc.expiry}, now)
			assert.Equal(t, tc.want, got)
		})
	}
}
