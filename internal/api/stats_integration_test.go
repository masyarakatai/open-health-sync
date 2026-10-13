// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type statsJSON struct {
	Type      string    `json:"type"`
	Interval  string    `json:"interval"`
	TZ        string    `json:"tz"`
	View      string    `json:"view"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Truncated bool      `json:"truncated"`
	Buckets   []struct {
		Start    time.Time `json:"start"`
		Provider string    `json:"provider"`
		Source   string    `json:"source"`
		Unit     *string   `json:"unit"`
		Count    int64     `json:"count"`
		Sum      float64   `json:"sum"`
		Avg      float64   `json:"avg"`
		Min      float64   `json:"min"`
		Max      float64   `json:"max"`
	} `json:"buckets"`
}

// sample inserts one quantity sample recorded by source.
func (e *env) statSample(tn, u uuid.UUID, qt, source string, value float64, at string) {
	e.t.Helper()
	e.exec(`INSERT INTO ohs_data.quantity_samples
		(tenant_id, user_id, provider, source_name, quantity_type, value, unit, start_date, content_hash)
		VALUES ($1, $2, 'apple_health', $3, $4, $5, CASE WHEN $4 = 'step_count' THEN 'count' ELSE 'count/min' END, $6, gen_random_uuid()::text)`,
		tn, u, source, qt, value, at)
}

func (e *env) refreshHourly() {
	e.t.Helper()
	e.exec(`CALL refresh_continuous_aggregate('ohs_data.quantity_hourly', NULL, NULL)`)
}

func (e *env) stats(path string) statsJSON {
	e.t.Helper()
	r := e.do("GET", path, e.keyA, nil)
	require.Equal(e.t, http.StatusOK, r.code, string(r.body))
	var s statsJSON
	r.json(e.t, &s)
	return s
}

type daySum struct {
	day, source string
	count       int64
	sum         float64
}

func daySums(s statsJSON) []daySum {
	out := []daySum{}
	for _, b := range s.Buckets {
		out = append(out, daySum{b.Start.Format(time.RFC3339), b.Source, b.Count, b.Sum})
	}
	return out
}

func TestSampleStatsDailyInTimeZone(t *testing.T) {
	e := newEnv(t)
	u := e.user(e.tenantA, "stats-1")
	// Paris is UTC+1 in March until the 29th. 23:30Z on March 1st is 00:30 on
	// March 2nd in Paris: it counts for the 2nd.
	e.statSample(e.tenantA, u, "step_count", "phone", 100, "2026-03-01T08:00:00Z")
	e.statSample(e.tenantA, u, "step_count", "phone", 50, "2026-03-01T22:30:00Z")
	e.statSample(e.tenantA, u, "step_count", "phone", 30, "2026-03-01T23:30:00Z")
	e.statSample(e.tenantA, u, "step_count", "watch", 120, "2026-03-01T08:05:00Z")
	e.statSample(e.tenantA, u, "step_count", "watch", 7, "2026-03-02T12:00:00Z")
	e.statSample(e.tenantA, u, "step_count", "phone", 999, "2026-03-03T12:00:00Z") // outside the range

	path := "/v1/users/" + u.String() + "/samples/stats?type=step_count&interval=day&tz=Europe/Paris&start=2026-03-01&end=2026-03-03&view=all"
	want := []daySum{
		{"2026-03-01T00:00:00+01:00", "phone", 2, 150},
		{"2026-03-01T00:00:00+01:00", "watch", 1, 120},
		{"2026-03-02T00:00:00+01:00", "phone", 1, 30},
		{"2026-03-02T00:00:00+01:00", "watch", 1, 7},
	}
	// Nothing materialized yet (real time), then materialized: same answer.
	s := e.stats(path)
	assert.Equal(t, want, daySums(s))
	assert.Equal(t, "Europe/Paris", s.TZ)
	assert.Equal(t, "2026-03-01T00:00:00+01:00", s.Start.Format(time.RFC3339))
	e.refreshHourly()
	assert.Equal(t, want, daySums(e.stats(path)))

	// A zone with a half-hour offset is read from the samples: in Kolkata
	// (UTC+5:30) 22:30Z and 23:30Z are on March 2nd.
	k := e.stats("/v1/users/" + u.String() + "/samples/stats?type=step_count&tz=Asia/Kolkata&start=2026-03-01&end=2026-03-03&view=all")
	assert.Equal(t, []daySum{
		{"2026-03-01T00:00:00+05:30", "phone", 1, 100},
		{"2026-03-01T00:00:00+05:30", "watch", 1, 120},
		{"2026-03-02T00:00:00+05:30", "phone", 2, 80},
		{"2026-03-02T00:00:00+05:30", "watch", 1, 7},
	}, daySums(k))

	// The merged view (the default) keeps one stream per day: the watch,
	// worn on the body, ranks before the phone of the same provider.
	merged := e.stats("/v1/users/" + u.String() + "/samples/stats?type=step_count&tz=Europe/Paris&start=2026-03-01&end=2026-03-03")
	assert.Equal(t, "merged", merged.View)
	assert.Equal(t, []daySum{
		{"2026-03-01T00:00:00+01:00", "watch", 1, 120},
		{"2026-03-02T00:00:00+01:00", "watch", 1, 7},
	}, daySums(merged))
	mk := e.stats("/v1/users/" + u.String() + "/samples/stats?type=step_count&tz=Asia/Kolkata&start=2026-03-01&end=2026-03-03")
	assert.Equal(t, []daySum{
		{"2026-03-01T00:00:00+05:30", "watch", 1, 120},
		{"2026-03-02T00:00:00+05:30", "watch", 1, 7},
	}, daySums(mk), "the samples path ranks the same way")

	// Late data in a materialized hour shows once the aggregate refreshes
	// (the policy does it every 5 minutes); a deletion likewise.
	e.statSample(e.tenantA, u, "step_count", "phone", 1, "2026-03-01T08:10:00Z")
	e.exec(`DELETE FROM ohs_data.quantity_samples WHERE tenant_id = $1 AND user_id = $2 AND source_name = 'watch'`, e.tenantA, u)
	// Until then the answer comes from the materialized hours: the aggregate
	// is what serves this zone.
	assert.Equal(t, want, daySums(e.stats(path)))
	e.refreshHourly()
	assert.Equal(t, []daySum{
		{"2026-03-01T00:00:00+01:00", "phone", 3, 151},
		{"2026-03-02T00:00:00+01:00", "phone", 1, 30},
	}, daySums(e.stats(path)))
}

func TestSampleStatsHourlyFigures(t *testing.T) {
	e := newEnv(t)
	u := e.user(e.tenantA, "stats-2")
	for _, v := range []struct {
		at string
		hr float64
	}{{"2026-03-01T08:00:00Z", 60}, {"2026-03-01T08:30:00Z", 90}, {"2026-03-01T09:15:00Z", 70}} {
		e.statSample(e.tenantA, u, "heart_rate", "watch", v.hr, v.at)
	}
	e.refreshHourly()
	s := e.stats("/v1/users/" + u.String() + "/samples/stats?type=heart_rate&interval=hour&start=2026-03-01T08:20:00Z&end=2026-03-01T09:20:00Z")
	// The range widens to whole hours: [08:00, 10:00).
	assert.Equal(t, "2026-03-01T08:00:00Z", s.Start.Format(time.RFC3339))
	assert.Equal(t, "2026-03-01T10:00:00Z", s.End.Format(time.RFC3339))
	require.Len(t, s.Buckets, 2)
	b := s.Buckets[0]
	assert.Equal(t, "2026-03-01T08:00:00Z", b.Start.Format(time.RFC3339))
	assert.Equal(t, int64(2), b.Count)
	assert.Equal(t, 75.0, b.Avg)
	assert.Equal(t, 60.0, b.Min)
	assert.Equal(t, 90.0, b.Max)
	require.NotNil(t, b.Unit)
	assert.Equal(t, "count/min", *b.Unit)
	assert.Equal(t, 70.0, s.Buckets[1].Avg)
}

func TestSampleStatsRefusals(t *testing.T) {
	e := newEnv(t)
	u := e.user(e.tenantA, "stats-3")
	base := "/v1/users/" + u.String() + "/samples/stats?"
	for q, want := range map[string]string{
		"interval=day&start=2026-03-01&end=2026-03-02":                    "type",
		"type=heart_rate&end=2026-03-02":                                  "start and end",
		"type=heart_rate&interval=week&start=2026-03-01&end=2026-03-02":   "interval",
		"type=heart_rate&tz=Mars/Olympus&start=2026-03-01&end=2026-03-02": "tz",
		"type=heart_rate&start=2026-03-02&end=2026-03-01":                 "before",
		"type=heart_rate&interval=hour&start=2020-01-01&end=2026-01-01":   "intervals",
		"type=heart_rate&view=best&start=2026-03-01&end=2026-03-02":       "view",
		"type=heartrate&start=2026-03-01&end=2026-03-02":                  "unknown type",
	} {
		r := e.do("GET", base+q, e.keyA, nil)
		require.Equal(t, http.StatusBadRequest, r.code, q)
		var p problem
		r.json(t, &p)
		assert.Contains(t, p.Detail, want, q)
	}
	// Another tenant's user is not found.
	r := e.do("GET", base+"type=heart_rate&start=2026-03-01&end=2026-03-02", e.keyB, nil)
	assert.Equal(t, http.StatusNotFound, r.code)
}
