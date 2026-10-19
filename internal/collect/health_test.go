// SPDX-License-Identifier: AGPL-3.0-only

package collect

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
)

func TestReauthReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"a missing scope is a missing scope", connector.ErrMissingScope, BrokenMissingScope},
		{"a wrapped missing scope stays one", fmt.Errorf("fetch: %w", connector.ErrMissingScope), BrokenMissingScope},
		{"a refused grant needs reauth", connector.ErrNeedsReauth, BrokenNeedsReauth},
		{"any other error needs reauth", errors.New("connection reset"), BrokenNeedsReauth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, reauthReason(tc.err))
		})
	}
}

func TestSevere(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		want   bool
	}{
		{"needs reauth is severe", BrokenNeedsReauth, true},
		{"a missing scope is severe", BrokenMissingScope, true},
		{"failing is not severe", BrokenFailing, false},
		{"rate limited is not severe", BrokenRateLimited, false},
		{"an unknown reason is not severe", "nonsense", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, severe(tc.reason))
		})
	}
}
