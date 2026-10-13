// SPDX-License-Identifier: AGPL-3.0-only

package rawstore

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Key builds the raw object key
// raw/<tenant>/<user>/<provider>/<yyyy-mm-dd>/<batch>_<part>.ndjson.gz.
// The day is formatted from the time's own calendar date, so the caller
// passes the intended day in the zone it means.
func Key(tenant, user uuid.UUID, provider string, day time.Time, batch string, part int) string {
	return fmt.Sprintf("raw/%s/%s/%s/%s/%s_%d.ndjson.gz",
		tenant, user, provider, day.Format("2006-01-02"), batch, part)
}

// validSegments reports whether every "/" separated segment of p is a clean
// name: not empty, not "." and without "..". A prefix may end with one "/"
// (its last segment is then complete) and may be a partial last segment.
func validSegments(p string, prefix bool) bool {
	if strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return false
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s == "." {
			return false
		}
		if s == "" && (!prefix || i != len(segs)-1) {
			return false
		}
	}
	return true
}

func checkKey(k string) error {
	if k == "" || !validSegments(k, false) || strings.HasPrefix(path.Base(k), tmpPrefix) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, k)
	}
	return nil
}

func checkPrefix(p string) error {
	if p != "" && !validSegments(p, true) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, p)
	}
	return nil
}

// checkDeletePrefix additionally requires at least three complete segments
// ("raw/<tenant>/<user>/" or deeper) so a bug cannot wipe the store or a tenant.
func checkDeletePrefix(p string) error {
	if err := checkPrefix(p); err != nil {
		return err
	}
	if strings.Count(p, "/") < 3 {
		return fmt.Errorf("%w: delete prefix %q is too broad", ErrInvalidKey, p)
	}
	return nil
}
