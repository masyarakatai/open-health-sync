// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import "time"

// SetNow replaces the clock of the default coverage window and returns a
// function that restores it.
func SetNow(f func() time.Time) (restore func()) {
	old := now
	now = f
	return func() { now = old }
}
