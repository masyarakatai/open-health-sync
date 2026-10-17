// SPDX-License-Identifier: AGPL-3.0-only

package selfreport

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/model"
)

const testID = "0192f1a2-0000-7000-8000-00000000c0de"

var at = time.Date(2026, 3, 1, 7, 30, 0, 0, time.UTC)

func report() Report {
	return Report{ID: testID, Kind: "feeling", Value: 4, StartAt: at, Timezone: "Europe/Paris"}
}

func TestKinds(t *testing.T) {
	assert.Equal(t, []string{"feeling", "mood", "stress", "energy", "sleep_quality", "muscle_soreness", "exertion", "pain"}, Kinds())
	for _, k := range Kinds() {
		qt, ok := QuantityType(k)
		require.True(t, ok, k)
		unit, ok := model.StorageUnit(qt)
		require.True(t, ok, qt)
		assert.Equal(t, "score", unit, qt)
	}
	for _, k := range []string{"", "reported_feeling", "happiness", "Feeling"} {
		_, ok := QuantityType(k)
		assert.False(t, ok, k)
	}
}

func TestCheck(t *testing.T) {
	end := at.Add(time.Hour)
	before := at.Add(-time.Minute)
	cases := map[string]struct {
		edit func(*Report)
		msg  string
	}{
		"valid":                {func(*Report) {}, ""},
		"session":              {func(r *Report) { r.Kind, r.Value, r.EndAt = "exertion", 7, &end }, ""},
		"zero on a 0-10 scale": {func(r *Report) { r.Kind, r.Value = "pain", 0 }, ""},
		"no time zone":         {func(r *Report) { r.Timezone = "" }, ""},
		"no id":                {func(r *Report) { r.ID = "" }, "id must be a UUID"},
		"bad id":               {func(r *Report) { r.ID = "42" }, "id must be a UUID"},
		"unknown kind":         {func(r *Report) { r.Kind = "joy" }, "kind must be one of feeling, mood"},
		"type as kind":         {func(r *Report) { r.Kind = "reported_feeling" }, "kind must be one of"},
		"below scale":          {func(r *Report) { r.Value = 0 }, "value must be a whole number from 1 to 5 for kind feeling"},
		"above scale":          {func(r *Report) { r.Kind, r.Value = "pain", 11 }, "from 0 to 10 for kind pain"},
		"not whole":            {func(r *Report) { r.Value = 3.5 }, "whole number"},
		"no start":             {func(r *Report) { r.StartAt = time.Time{} }, "start_at is required"},
		"start too early":      {func(r *Report) { r.StartAt = time.Date(1850, 1, 1, 0, 0, 0, 0, time.UTC) }, "between 1900 and 2200"},
		"end before start":     {func(r *Report) { r.EndAt = &before }, "end_at must be after start_at"},
		"end equal to start":   {func(r *Report) { r.EndAt = &at }, "end_at must be after start_at"},
		"bad zone":             {func(r *Report) { r.Timezone = "Mars/Olympus" }, "timezone must be an IANA"},
		"local zone":           {func(r *Report) { r.Timezone = "Local" }, "timezone must be an IANA"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := report()
			c.edit(&r)
			err := r.Check()
			if c.msg == "" {
				assert.NoError(t, err)
				return
			}
			var ie *InvalidError
			require.ErrorAs(t, err, &ie)
			assert.Contains(t, ie.Msg, c.msg)
		})
	}
}

func TestCheckNormalizes(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	require.NoError(t, err)
	r := Report{ID: strings.ToUpper(testID), Kind: "mood", Value: 2, StartAt: time.Date(2026, 3, 1, 8, 30, 0, 1999, paris)}
	require.NoError(t, r.Check())
	assert.Equal(t, testID, r.ID)
	assert.Equal(t, time.UTC, r.StartAt.Location())
	assert.Equal(t, time.Date(2026, 3, 1, 7, 30, 0, 1000, time.UTC), r.StartAt)
}

func TestDecode(t *testing.T) {
	r, err := Decode([]byte(`{"id":"` + strings.ToUpper(testID) + `","kind":"mood","value":2,"start_at":"2026-03-01T08:30:00+01:00"}`))
	require.NoError(t, err)
	assert.Equal(t, Report{ID: testID, Kind: "mood", Value: 2, StartAt: at}, r)

	for name, line := range map[string]string{
		"not json":       `feeling=4`,
		"unknown member": `{"id":"` + testID + `","kind":"feeling","value":4,"start_at":"2026-03-01T07:30:00Z","vlaue":5}`,
		"two values":     `{"id":"` + testID + `","kind":"feeling","value":4,"start_at":"2026-03-01T07:30:00Z"} {}`,
		"off scale":      `{"id":"` + testID + `","kind":"feeling","value":9,"start_at":"2026-03-01T07:30:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode([]byte(line))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), testID, "the message never quotes the data")
		})
	}
}

func TestLineRoundTrip(t *testing.T) {
	end := at.Add(45 * time.Minute)
	r := Report{ID: testID, Kind: "exertion", Value: 7, StartAt: at, EndAt: &end, Timezone: "Europe/Paris"}
	l, err := Line(r)
	require.NoError(t, err)
	assert.Equal(t, byte('\n'), l[len(l)-1])
	back, err := Decode(l)
	require.NoError(t, err)
	assert.Equal(t, r, back)
}

func TestSample(t *testing.T) {
	r := report()
	s := Sample("0192f1a2-0000-7000-8000-000000000001", r)
	assert.Equal(t, model.QTReportedFeeling, s.QuantityType)
	assert.Equal(t, "score", s.Unit)
	assert.Equal(t, s.StartDate, s.EndDate, "a rating without end_at is an instant")
	r.Value = 2
	assert.Equal(t, s.ContentHash, Sample("0192f1a2-0000-7000-8000-000000000001", r).ContentHash, "the value is not part of the identity")
	assert.NotEqual(t, s.ContentHash, Sample("0192f1a2-0000-7000-8000-000000000002", r).ContentHash)
}
