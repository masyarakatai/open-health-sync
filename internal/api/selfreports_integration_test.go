// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/connectors/selfreport"
	"github.com/soma-intelligence/open-health-sync/internal/api"
	"github.com/soma-intelligence/open-health-sync/internal/etl"
	"github.com/soma-intelligence/open-health-sync/internal/fhir"
	"github.com/soma-intelligence/open-health-sync/internal/queue"
	"github.com/soma-intelligence/open-health-sync/internal/rawstore"
)

type selfReportJSON struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Type     string     `json:"type"`
	Value    float64    `json:"value"`
	StartAt  time.Time  `json:"start_at"`
	EndAt    *time.Time `json:"end_at"`
	Timezone *string    `json:"timezone"`
}

type selfReportsJSON struct {
	Reports   []selfReportJSON `json:"reports"`
	Duplicate bool             `json:"duplicate"`
}

// srEnv is an env whose router serves self reports, with the raw store the
// ETL reads them back from.
type srEnv struct {
	*env
	raw rawstore.Store
}

func newSelfReportEnv(t *testing.T) *srEnv {
	t.Helper()
	var raw rawstore.Store
	e := newEnv(t, func(d *api.Deps) {
		d.SelfReport = selfreport.New()
		d.PublicURL = publicURL + "/"
		raw = d.Raw
	})
	return &srEnv{env: e, raw: raw}
}

func (e *srEnv) report(user uuid.UUID, key, body string, hdr ...string) (resp, selfReportsJSON) {
	e.t.Helper()
	r := e.do("POST", "/v1/users/"+user.String()+"/self-reports", key, body, hdr...)
	var out selfReportsJSON
	if r.code == http.StatusAccepted {
		r.json(e.t, &out)
	}
	return r, out
}

// processAll runs the ETL over every queued upload job, as the worker would.
func (e *srEnv) processAll() int {
	e.t.Helper()
	ctx := context.Background()
	q := queue.NewETL(e.pool)
	n := 0
	for {
		jobs, err := q.Claim(ctx, queue.TierHighFreq, 10)
		require.NoError(e.t, err)
		if len(jobs) == 0 {
			return n
		}
		for _, j := range jobs {
			_, err := etl.Process(ctx, e.pool, e.raw, j)
			require.NoError(e.t, err)
			require.NoError(e.t, q.Complete(ctx, j))
			n++
		}
	}
}

type sampleRow struct {
	ID             int64     `json:"id"`
	Type           string    `json:"type"`
	Value          float64   `json:"value"`
	Unit           string    `json:"unit"`
	StartDate      time.Time `json:"start_date"`
	EndDate        time.Time `json:"end_date"`
	Source         *string   `json:"source"`
	Provider       string    `json:"provider"`
	ExternalID     *string   `json:"external_id"`
	WasUserEntered *bool     `json:"was_user_entered"`
}

func (e *srEnv) samples(user uuid.UUID, qt string) []sampleRow {
	e.t.Helper()
	r := e.do("GET", "/v1/users/"+user.String()+"/samples?type="+qt, e.keyA, nil)
	require.Equal(e.t, http.StatusOK, r.code, string(r.body))
	var p page[sampleRow]
	r.json(e.t, &p)
	return p.Data
}

const (
	srFeeling  = "0192f1a2-0000-7000-8000-00000000a001"
	srExertion = "0192f1a2-0000-7000-8000-00000000a002"
)

func TestSelfReportsRoundTrip(t *testing.T) {
	e := newSelfReportEnv(t)
	u := e.user(e.tenantA, "rater")
	body := `{"reports":[
		{"id":"` + srFeeling + `","kind":"feeling","value":4,"start_at":"2026-03-01T08:30:00+01:00","timezone":"Europe/Paris"},
		{"id":"` + srExertion + `","kind":"exertion","value":7,"start_at":"2026-03-01T17:00:00Z","end_at":"2026-03-01T18:00:00Z"}]}`

	r, out := e.report(u, e.keyA, body)
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	assert.False(t, out.Duplicate)
	require.Len(t, out.Reports, 2)
	assert.Equal(t, selfReportJSON{ID: srFeeling, Kind: "feeling", Type: "reported_feeling", Value: 4,
		StartAt: time.Date(2026, 3, 1, 7, 30, 0, 0, time.UTC), Timezone: strPtr("Europe/Paris")}, out.Reports[0])
	assert.Equal(t, "reported_exertion", out.Reports[1].Type)
	require.NotNil(t, out.Reports[1].EndAt)
	assert.Nil(t, out.Reports[1].Timezone)

	// The same reports again are the same upload part.
	r, again := e.report(u, e.keyA, body)
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	assert.True(t, again.Duplicate)
	assert.Equal(t, out.Reports, again.Reports)

	assert.Equal(t, 1, e.processAll())
	feel := e.samples(u, "reported_feeling")
	require.Len(t, feel, 1)
	assert.Equal(t, 4.0, feel[0].Value)
	assert.Equal(t, "score", feel[0].Unit)
	assert.Equal(t, "reported_feeling", feel[0].Type)
	assert.Equal(t, "self_report", feel[0].Provider)
	require.NotNil(t, feel[0].ExternalID)
	assert.Equal(t, srFeeling, *feel[0].ExternalID)
	require.NotNil(t, feel[0].WasUserEntered)
	assert.True(t, *feel[0].WasUserEntered)
	ex := e.samples(u, "reported_exertion")
	require.Len(t, ex, 1)
	assert.Equal(t, time.Hour, ex[0].EndDate.Sub(ex[0].StartDate), "a rated session keeps its period")

	// FHIR: category survey, the person as performer.
	obs := e.walk("/v1/users/"+u.String()+"/fhir/Observation?category=survey", e.keyA)
	require.Len(t, obs, 2)
	for _, o := range obs {
		require.Len(t, o.Category, 1)
		assert.Equal(t, fhir.CategorySurvey, o.Category[0].Coding[0].Code)
		require.Len(t, o.Performer, 1)
		assert.Equal(t, "Patient/"+u.String(), o.Performer[0].Reference)
	}

	// A correction: same id, new value. Then the start moved. One sample
	// remains, with the latest value and start.
	r, _ = e.report(u, e.keyA, `{"reports":[{"id":"`+srFeeling+`","kind":"feeling","value":2,"start_at":"2026-03-01T07:30:00Z"}]}`)
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	e.processAll()
	feel = e.samples(u, "reported_feeling")
	require.Len(t, feel, 1)
	assert.Equal(t, 2.0, feel[0].Value)
	r, _ = e.report(u, e.keyA, `{"reports":[{"id":"`+srFeeling+`","kind":"feeling","value":3,"start_at":"2026-03-01T09:00:00Z"}]}`)
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	e.processAll()
	feel = e.samples(u, "reported_feeling")
	require.Len(t, feel, 1, "a moved report replaces the old sample")
	assert.Equal(t, 3.0, feel[0].Value)
	assert.Equal(t, time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC), feel[0].StartDate.UTC())

	// The daily mean of a rating, in the person's zone.
	e.refreshHourly()
	st := e.stats("/v1/users/" + u.String() + "/samples/stats?type=reported_feeling&interval=day&tz=Europe/Paris&start=2026-03-01&end=2026-03-02")
	require.Len(t, st.Buckets, 1)
	assert.Equal(t, 3.0, st.Buckets[0].Avg)
	assert.Equal(t, int64(1), st.Buckets[0].Count)

	// A report without an id gets one.
	r, gen := e.report(u, e.keyA, `{"reports":[{"kind":"pain","value":0,"start_at":"2026-03-02T07:00:00Z"}]}`)
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	_, err := uuid.Parse(gen.Reports[0].ID)
	assert.NoError(t, err)
	e.processAll()
	pain := e.samples(u, "reported_pain")
	require.Len(t, pain, 1)
	assert.Equal(t, 0.0, pain[0].Value)
}

func TestSelfReportsAccess(t *testing.T) {
	e := newSelfReportEnv(t)
	u := e.user(e.tenantA, "rater")
	other := e.user(e.tenantA, "someone-else")
	body := `{"reports":[{"kind":"mood","value":3,"start_at":"2026-03-01T07:30:00Z"}]}`
	mint := func(user uuid.UUID, scope string) string {
		r := e.do("POST", "/v1/users/"+user.String()+"/tokens", e.keyA, map[string]any{"scopes": []string{scope}})
		require.Equal(t, http.StatusCreated, r.code, string(r.body))
		var tok struct{ Token string }
		r.json(t, &tok)
		return tok.Token
	}
	upload := mint(u, "upload")

	r, _ := e.report(u, upload, body)
	assert.Equal(t, http.StatusAccepted, r.code, "the person's own app: %s", r.body)
	r, _ = e.report(other, upload, body)
	assert.Equal(t, http.StatusNotFound, r.code, "a user token never writes for another user")
	r, _ = e.report(u, mint(u, "read"), body)
	assert.Equal(t, http.StatusForbidden, r.code, "read scope")
	r, _ = e.report(u, e.keyB, body)
	assert.Equal(t, http.StatusNotFound, r.code, "another tenant's user")
	r, _ = e.report(uuid.New(), e.keyA, body)
	assert.Equal(t, http.StatusNotFound, r.code, "no such user")
	r, _ = e.report(u, "", body)
	assert.Equal(t, http.StatusUnauthorized, r.code)
	r = e.do("POST", "/v1/users/not-a-uuid/self-reports", e.keyA, body)
	assert.Equal(t, http.StatusNotFound, r.code)

	// A user being deleted takes no report.
	e.exec(`UPDATE ohs_core.end_users SET status = 'deleting' WHERE id = $1`, other)
	r, _ = e.report(other, e.keyA, body)
	assert.Equal(t, http.StatusNotFound, r.code, string(r.body))
}

func TestSelfReportsValidation(t *testing.T) {
	e := newSelfReportEnv(t)
	u := e.user(e.tenantA, "rater")
	ok := `"kind":"feeling","value":4,"start_at":"2026-03-01T07:30:00Z"`
	var many strings.Builder
	many.WriteString(`{"reports":[`)
	for i := range 101 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString("{" + ok + "}")
	}
	many.WriteString("]}")

	for body, want := range map[string]string{
		`{"reports":[]}`: "at least one report",
		`{}`:             "at least one report",
		many.String():    "at most 100 reports",
		`{"reports":[{"kind":"joy","value":4,"start_at":"2026-03-01T07:30:00Z"}]}`:                       "reports[0].kind must be one of feeling, mood",
		`{"reports":[{"kind":"feeling","start_at":"2026-03-01T07:30:00Z"}]}`:                             "reports[0].value is required",
		`{"reports":[{"kind":"feeling","value":4}]}`:                                                     "reports[0].start_at is required",
		`{"reports":[{"kind":"feeling","value":4,"start_at":"2026-03-01"}]}`:                             "reports[0].start_at must be an RFC 3339 time",
		`{"reports":[{` + ok + `},{"kind":"stress","value":6,"start_at":"2026-03-01T07:30:00Z"}]}`:       "reports[1].value must be a whole number from 1 to 5 for kind stress",
		`{"reports":[{"kind":"pain","value":2.5,"start_at":"2026-03-01T07:30:00Z"}]}`:                    "whole number from 0 to 10",
		`{"reports":[{` + ok + `,"end_at":"2026-03-01T07:00:00Z"}]}`:                                     "end_at must be after start_at",
		`{"reports":[{` + ok + `,"timezone":"Mars/Olympus"}]}`:                                           "timezone must be an IANA",
		`{"reports":[{` + ok + `,"id":"42"}]}`:                                                           "id must be a UUID",
		`{"reports":[{` + ok + `,"id":"` + srFeeling + `"},{` + ok + `,"id":"` + srFeeling + `"}]}`:      "reports[1].id is used by another report",
		`{"reports":[{` + ok + `,"note":"tired"}]}`:                                                      "unknown field",
		`{"reports":[{"kind":"feeling","value":"4","start_at":"2026-03-01T07:30:00Z"}]}`:                 "not valid JSON",
		`{"reports":[{"kind":"feeling","value":4,"start_at":"1850-03-01T07:30:00Z"}]}`:                   "between 1900 and 2200",
		`{"reports":[{"kind":"exertion","value":9,"start_at":"2026-03-01T07:30:00Z","end_at":"later"}]}`: "end_at must be an RFC 3339 time",
	} {
		r, _ := e.report(u, e.keyA, body)
		require.Equal(t, http.StatusBadRequest, r.code, "%.80s: %s", body, r.body)
		assert.Equal(t, "application/problem+json", r.header.Get("Content-Type"))
		var p struct{ Detail string }
		require.NoError(t, json.Unmarshal(r.body, &p))
		assert.Contains(t, p.Detail, want, body)
		assert.NotContains(t, p.Detail, "07:30", "a problem never quotes the data")
		assert.NotContains(t, p.Detail, srFeeling, "a problem never quotes the data")
	}
	assert.Zero(t, e.processAll(), "nothing refused was stored")
}

func TestSelfReportsIdempotencyKey(t *testing.T) {
	e := newSelfReportEnv(t)
	u := e.user(e.tenantA, "rater")
	body := `{"reports":[{"kind":"energy","value":2,"start_at":"2026-03-01T07:30:00Z"}]}`
	r, first := e.report(u, e.keyA, body, "Idempotency-Key", "sr-1")
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	r, replay := e.report(u, e.keyA, body, "Idempotency-Key", "sr-1")
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	assert.Equal(t, "true", r.header.Get("Idempotent-Replayed"))
	assert.Equal(t, first.Reports[0].ID, replay.Reports[0].ID, "the generated id is replayed, not generated again")
	r, _ = e.report(u, e.keyA, strings.Replace(body, `"value":2`, `"value":3`, 1), "Idempotency-Key", "sr-1")
	assert.Equal(t, http.StatusUnprocessableEntity, r.code, string(r.body))
	e.processAll()
	assert.Len(t, e.samples(u, "reported_energy"), 1)
}

func TestSelfReportsConsent(t *testing.T) {
	e := newSelfReportEnv(t)
	u := e.user(e.tenantA, "rater")
	r := e.do("PUT", "/v1/users/"+u.String()+"/consents/self_report", e.keyA, `{"granted":["sleep"]}`)
	require.Equal(t, http.StatusOK, r.code, string(r.body))
	r, _ = e.report(u, e.keyA, `{"reports":[
		{"kind":"sleep_quality","value":4,"start_at":"2026-03-01T07:00:00Z"},
		{"kind":"mood","value":2,"start_at":"2026-03-01T07:00:00Z"}]}`)
	require.Equal(t, http.StatusAccepted, r.code, string(r.body))
	e.processAll()
	assert.Len(t, e.samples(u, "reported_sleep_quality"), 1, "sleep is granted")
	assert.Empty(t, e.samples(u, "reported_mood"), "mental is not granted: withheld")
}

func TestSelfReportsRouteNeedsTheConnector(t *testing.T) {
	e := newEnv(t)
	u := e.user(e.tenantA, "rater")
	r := e.do("POST", "/v1/users/"+u.String()+"/self-reports", e.keyA, `{"reports":[]}`)
	assert.Equal(t, http.StatusNotFound, r.code, "not mounted without the connector")
}

func strPtr(s string) *string { return &s }
