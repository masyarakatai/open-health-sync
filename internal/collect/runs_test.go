// SPDX-License-Identifier: AGPL-3.0-only

package collect

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/soma-intelligence/open-health-sync/internal/queue"
)

func TestRunStatus(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name    string
		status  string
		outcome *string
		want    string
	}{
		{"running is running", "running", nil, RunRunning},
		{"running ignores its outcome", "running", str(queue.OutcomeRateLimited), RunRunning},
		{"done succeeded", "done", nil, RunSucceeded},
		{"done ignores its outcome", "done", str(queue.OutcomeFailed), RunSucceeded},
		{"parked and inactive is cancelled", "parked", str(queue.OutcomeInactive), RunCancelled},
		{"parked without an outcome failed", "parked", nil, RunFailed},
		{"parked rate limited failed", "parked", str(queue.OutcomeRateLimited), RunFailed},
		{"parked completed failed", "parked", str(queue.OutcomeCompleted), RunFailed},
		{"queued without an outcome waits", "queued", nil, RunQueued},
		{"queued and rate limited waits", "queued", str(queue.OutcomeRateLimited), RunRateLimited},
		{"queued and failed retries", "queued", str(queue.OutcomeFailed), RunRetrying},
		{"queued and completed waits", "queued", str(queue.OutcomeCompleted), RunQueued},
		{"queued and inactive waits", "queued", str(queue.OutcomeInactive), RunQueued},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, runStatus(tc.status, tc.outcome))
		})
	}
}
