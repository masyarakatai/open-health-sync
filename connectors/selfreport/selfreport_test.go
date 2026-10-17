// SPDX-License-Identifier: AGPL-3.0-only

package selfreport

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
	"github.com/soma-intelligence/open-health-sync/internal/model"
	report "github.com/soma-intelligence/open-health-sync/internal/selfreport"
)

const (
	testUser = "0192f1a2-0000-7000-8000-000000000001"
	testID   = "0192f1a2-0000-7000-8000-00000000c0de"
)

var at = time.Date(2026, 3, 1, 7, 30, 0, 0, time.UTC)

func feeling() report.Report {
	return report.Report{ID: testID, Kind: "feeling", Value: 4, StartAt: at, Timezone: "Europe/Paris"}
}

func lines(t *testing.T, rs ...report.Report) string {
	t.Helper()
	var b strings.Builder
	for _, r := range rs {
		l, err := report.Line(r)
		require.NoError(t, err)
		b.Write(l)
	}
	return b.String()
}

func TestParseUpload(t *testing.T) {
	c := New()
	parse := func(body string) error { return c.ParseUpload(connector.UploadMeta{}, strings.NewReader(body), nil) }

	pain := feeling()
	pain.ID, pain.Kind, pain.Value = "0192f1a2-0000-7000-8000-00000000c0df", "pain", 2
	assert.NoError(t, parse(lines(t, feeling(), pain)))
	assert.NoError(t, parse("\n"+lines(t, feeling())+"\n\n"), "blank lines are skipped")

	assert.ErrorIs(t, parse(""), ErrNoLines)
	assert.ErrorIs(t, parse("\n \n"), ErrNoLines)

	for name, body := range map[string]string{
		"not json":       "feeling=4\n",
		"array":          `[{"id":"` + testID + `"}]` + "\n",
		"unknown member": `{"id":"` + testID + `","kind":"feeling","value":4,"start_at":"2026-03-01T07:30:00Z","vlaue":5}` + "\n",
		"two values":     `{"id":"` + testID + `","kind":"feeling","value":4,"start_at":"2026-03-01T07:30:00Z"} {}` + "\n",
		"off scale":      `{"id":"` + testID + `","kind":"feeling","value":9,"start_at":"2026-03-01T07:30:00Z"}` + "\n",
		"too long":       `{"id":"` + testID + `","kind":"feeling","value":4,"start_at":"2026-03-01T07:30:00Z","timezone":"` + strings.Repeat("x", report.MaxLineBytes) + `"}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			err := parse(lines(t, feeling()) + body)
			require.ErrorIs(t, err, ErrInvalidLine)
			assert.Contains(t, err.Error(), "line 2")
			assert.NotContains(t, err.Error(), testID, "the message never quotes the data")
			assert.NotContains(t, err.Error(), "07:30", "the message never quotes the data")
		})
	}
}

func mapLine(t *testing.T, line []byte) (model.MapResult, error) {
	t.Helper()
	var fields map[string]any
	require.NoError(t, json.Unmarshal(line, &fields))
	return New().Map(connector.RawRecord{UserID: testUser, Provider: Slug, DataType: report.DataType, Fields: fields})
}

func TestMap(t *testing.T) {
	end := at.Add(45 * time.Minute)
	r := report.Report{ID: testID, Kind: "exertion", Value: 7, StartAt: at, EndAt: &end, Timezone: "Europe/Paris"}
	require.NoError(t, r.Check())
	l, err := report.Line(r)
	require.NoError(t, err)

	mr, err := mapLine(t, l)
	require.NoError(t, err)
	require.Len(t, mr.QuantitySamples, 1)
	s := mr.QuantitySamples[0]
	assert.Equal(t, "reported_exertion", s.QuantityType)
	assert.Equal(t, 7.0, s.Value)
	assert.Equal(t, "score", s.Unit)
	assert.Equal(t, Slug, s.Provider)
	assert.Equal(t, Slug, s.SourceSlug)
	assert.Equal(t, testID, s.ExternalID)
	assert.Equal(t, at, s.StartDate)
	assert.Equal(t, end, s.EndDate)
	require.NotNil(t, s.WasUserEntered)
	assert.True(t, *s.WasUserEntered)
	assert.True(t, s.Replaceable)
	assert.True(t, s.SupersedeMoved)
	assert.Equal(t, "Europe/Paris", s.Metadata["timezone"])
	assert.NotEmpty(t, s.ContentHash)

	// A correction (same id, new value) keeps the identity, so it replaces
	// the stored rating; another report gets another identity.
	r.Value = 5
	l2, err := report.Line(r)
	require.NoError(t, err)
	mr2, err := mapLine(t, l2)
	require.NoError(t, err)
	assert.Equal(t, s.ContentHash, mr2.QuantitySamples[0].ContentHash)
	r.ID = "0192f1a2-0000-7000-8000-00000000c0df"
	l3, err := report.Line(r)
	require.NoError(t, err)
	mr3, err := mapLine(t, l3)
	require.NoError(t, err)
	assert.NotEqual(t, s.ContentHash, mr3.QuantitySamples[0].ContentHash)
}

func TestMapInstantWithoutZone(t *testing.T) {
	r := report.Report{ID: testID, Kind: "feeling", Value: 3, StartAt: at}
	l, err := report.Line(r)
	require.NoError(t, err)
	mr, err := mapLine(t, l)
	require.NoError(t, err)
	s := mr.QuantitySamples[0]
	assert.Equal(t, s.StartDate, s.EndDate)
	assert.NotContains(t, s.Metadata, "timezone")
}

func TestMapRefusesAStoredLineThatNoLongerChecks(t *testing.T) {
	_, err := mapLine(t, []byte(`{"id":"`+testID+`","kind":"feeling","value":6,"start_at":"2026-03-01T07:30:00Z"}`))
	assert.True(t, errors.Is(err, ErrInvalidLine))
}

func TestRegistered(t *testing.T) {
	c, ok := connector.Get(Slug)
	require.True(t, ok)
	_, ok = connector.AsDevice(c)
	assert.True(t, ok)
}
