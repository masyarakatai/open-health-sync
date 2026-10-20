// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/api/read"
)

func TestNewPage(t *testing.T) {
	empty := newPage[string](nil, "")
	got, err := json.Marshal(empty)
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":[],"next_cursor":null}`, string(got), "nil data answers [] with no cursor")

	last := newPage([]int{1, 2}, "")
	got, err = json.Marshal(last)
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":[1,2],"next_cursor":null}`, string(got))

	page := newPage([]string{"a"}, "next-1")
	require.NotNil(t, page.NextCursor, "a next cursor is set")
	assert.Equal(t, "next-1", *page.NextCursor)
	got, err = json.Marshal(page)
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":["a"],"next_cursor":"next-1"}`, string(got))
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		name    string
		query   string
		want    int
		wantErr bool
	}{
		{"absent takes the default", "", 0, false},
		{"a limit in range", "?limit=5", 5, false},
		{"one is the smallest limit", "?limit=1", 1, false},
		{"the maximum is allowed", fmt.Sprintf("?limit=%d", read.MaxLimit), read.MaxLimit, false},
		{"zero is refused", "?limit=0", 0, true},
		{"below one is refused", "?limit=-2", 0, true},
		{"over the maximum is refused", fmt.Sprintf("?limit=%d", read.MaxLimit+1), 0, true},
		{"not a number is refused", "?limit=many", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLimit(httptest.NewRequest("GET", "/v1/users"+tc.query, nil))
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "between 1 and")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseTimeIn(t *testing.T) {
	loc := time.FixedZone("UTC+7", 7*3600)

	got, err := parseTimeIn("2026-03-01", loc)
	require.NoError(t, err, "a calendar date is midnight in loc")
	assert.Equal(t, 2026, got.Year())
	assert.Equal(t, time.March, got.Month())
	assert.Equal(t, 1, got.Day())
	assert.Equal(t, 0, got.Hour())
	assert.Equal(t, "UTC+7", got.Location().String())

	got, err = parseTimeIn("2026-03-01T10:00:00Z", loc)
	require.NoError(t, err, "an RFC 3339 time keeps its own zone")
	assert.Equal(t, "UTC", got.Location().String())
	assert.Equal(t, 10, got.Hour())

	_, err = parseTimeIn("2026-3-1", loc)
	require.Error(t, err)
}

func TestParseReadQuery(t *testing.T) {
	q, err := parseReadQuery(httptest.NewRequest("GET",
		"/v1/users/u/data?type=sleep&start=2026-03-01&end=2026-03-31T10:00:00Z&cursor=abc&limit=10&view=all", nil))
	require.NoError(t, err)
	assert.Equal(t, "sleep", q.Type)
	assert.Equal(t, "abc", q.Cursor)
	assert.Equal(t, 10, q.Limit)
	assert.Equal(t, read.ViewAll, q.View)
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), q.Start.UTC())
	assert.Equal(t, time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC), q.End.UTC())

	empty, err := parseReadQuery(httptest.NewRequest("GET", "/v1/users/u/data", nil))
	require.NoError(t, err, "an empty query is valid")
	assert.Equal(t, "", empty.Type)
	assert.Equal(t, "", empty.Cursor)
	assert.Equal(t, 0, empty.Limit)
	assert.Equal(t, read.ViewMerged, empty.View, "the view defaults to merged")
	assert.True(t, empty.Start.IsZero(), "an open start bound stays zero")
	assert.True(t, empty.End.IsZero(), "an open end bound stays zero")

	bad := []struct {
		name  string
		query string
		want  string
	}{
		{"an unknown view", "?view=raw", "view must be merged or all"},
		{"a bad limit", "?limit=0", "limit must be an integer"},
		{"a bad start", "?start=yesterday", "start:"},
		{"a bad end", "?end=2026-3-1", "end:"},
	}
	for _, tc := range bad {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			_, err := parseReadQuery(httptest.NewRequest("GET", "/v1/users/u/data"+tc.query, nil))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
