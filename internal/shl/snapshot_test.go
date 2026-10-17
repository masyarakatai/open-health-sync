// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/api/read"
	"github.com/soma-intelligence/open-health-sync/internal/fhir"
)

var (
	snapUser = uuid.MustParse("00000000-0000-4000-8000-0000000000a1")
	snapAt   = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	snapT0   = time.Date(2026, 10, 1, 7, 30, 0, 0, time.UTC)
)

const snapBase = "https://ohs.example/v1/users/00000000-0000-4000-8000-0000000000a1/fhir"

func ptr[T any](v T) *T { return &v }

// invented records, one per kind.
func quantity(id int64, provider, qt string, v float64, unit string) read.ObservationRecord {
	s := &read.CodedSample{Sample: read.Sample{ID: id, Type: qt, Value: v, Unit: ptr(unit),
		StartDate: snapT0.Add(time.Duration(id) * time.Minute), DeviceModel: ptr("Invented Ring 2"),
		Provenance: read.Provenance{Provider: provider, Source: ptr("invented"), Device: ptr("ring-1"), IngestedAt: snapAt}}}
	s.StorageUnit = ptr(unit)
	return read.ObservationRecord{Kind: read.KindQuantity, Sample: s}
}

func panel(id int64) read.ObservationRecord {
	sys := quantity(id, "invented_cuff", read.Systolic, 118, "mmHg")
	dia := quantity(id+1, "invented_cuff", read.Diastolic, 76, "mmHg")
	sys.Partner = dia.Sample
	return sys
}

func sleepRecord(id int64, provider string) read.ObservationRecord {
	return read.ObservationRecord{Kind: read.KindSleep, Sleep: &read.SleepSession{ID: id, BedtimeStart: snapT0,
		BedtimeEnd: ptr(snapT0.Add(7 * time.Hour)), TotalSleepSeconds: ptr(25200),
		Provenance: read.Provenance{Provider: provider, IngestedAt: snapAt}}}
}

func workoutRecord(id int64) read.ObservationRecord {
	return read.ObservationRecord{Kind: read.KindWorkout, Workout: &read.Workout{ID: id, ActivityType: "running",
		StartDate: snapT0, EndDate: ptr(snapT0.Add(time.Hour)),
		Provenance: read.Provenance{Provider: "invented_watch", IngestedAt: snapAt}}}
}

func dailyRecord(id int64) read.ObservationRecord {
	return read.ObservationRecord{Kind: read.KindDaily, Daily: &read.DailySummary{ID: id, Date: "2026-10-01",
		StepCount: ptr(4321), Provenance: read.Provenance{Provider: "invented_watch", IngestedAt: snapAt}}}
}

func TestRecordCategory(t *testing.T) {
	for want, rec := range map[string]read.ObservationRecord{
		"heart":          quantity(1, "p", "heart_rate", 61, "count/min"),
		"blood_pressure": panel(2),
		"glucose":        quantity(4, "p", "blood_glucose", 5.1, "mmol/L"),
		"body":           quantity(5, "p", "body_mass", 70, "kg"),
		"respiratory":    quantity(6, "p", "respiratory_rate", 14, "count/min"),
		"other":          quantity(7, "p", "invented_metric", 1, "1"),
		"sleep":          sleepRecord(8, "p"),
	} {
		assert.Equal(t, want, RecordCategory(rec), want)
	}
	assert.Equal(t, "activity", RecordCategory(quantity(9, "p", "step_count", 100, "count")))
	assert.Equal(t, "activity", RecordCategory(workoutRecord(10)))
	assert.Equal(t, "activity", RecordCategory(dailyRecord(11)))
	assert.Equal(t, "blood_pressure", RecordCategory(quantity(12, "p", read.Diastolic, 70, "mmHg")), "an unpaired half")
	assert.Equal(t, "invented_cuff", RecordProvider(panel(13)))
	assert.Equal(t, "invented_watch", RecordProvider(dailyRecord(14)))
}

func TestFilter(t *testing.T) {
	types := []string{"heart_rate", "step_count", read.Systolic, read.Diastolic, "body_mass", "invented_metric"}
	f := Filter([]string{"heart"}, types)
	assert.Equal(t, read.ObservationFilter{Quantities: true, Types: []string{"heart_rate"}}, f)

	f = Filter([]string{"activity", "sleep"}, types)
	assert.Equal(t, read.ObservationFilter{Quantities: true, Types: []string{"step_count"}, Sleep: true,
		Workouts: true, Daily: true}, f)

	f = Filter([]string{"blood_pressure", "other"}, types)
	assert.Equal(t, []string{read.Systolic, read.Diastolic, "invented_metric"}, f.Types)

	// No quantity type of the category: a filter that keeps no sample, never
	// one that keeps them all.
	f = Filter([]string{"glucose"}, types)
	assert.True(t, f.Empty())
	assert.NotNil(t, f.Types)
	f = Filter([]string{"sleep"}, nil)
	assert.Equal(t, read.ObservationFilter{Quantities: true, Types: []string{}, Sleep: true}, f)
}

type bundleJSON struct {
	ResourceType string `json:"resourceType"`
	Type         string `json:"type"`
	Timestamp    string `json:"timestamp"`
	Entry        []struct {
		FullURL  string          `json:"fullUrl"`
		Resource json.RawMessage `json:"resource"`
		Search   any             `json:"search"`
	} `json:"entry"`
}

func TestSnapshot(t *testing.T) {
	var buf bytes.Buffer
	snap, err := NewSnapshot(&buf, SnapshotOptions{User: snapUser, Base: snapBase, At: snapAt,
		Categories: []string{"heart", "blood_pressure", "sleep"},
		Skip:       func(provider, category string) bool { return provider == "invented_ring" && category == "sleep" }})
	require.NoError(t, err)
	hr1 := quantity(1, "invented_ring", "heart_rate", 61, "count/min")
	hr2 := quantity(2, "invented_ring", "heart_rate", 63, "count/min")
	for _, rec := range []read.ObservationRecord{
		hr1,
		panel(3),
		sleepRecord(5, "invented_ring"),  // skipped: its purge is pending
		sleepRecord(6, "invented_watch"), // kept
		workoutRecord(7),                 // activity: not shared
		quantity(8, "invented_ring", "step_count", 100, "count"),
		hr2,
	} {
		require.NoError(t, snap.Add(rec))
	}
	require.NoError(t, snap.Close())
	assert.Equal(t, 4, snap.Observations())
	assert.Equal(t, []string{"invented_cuff/blood_pressure", "invented_ring/heart", "invented_watch/sleep"}, snap.Sources())

	var b bundleJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &b), buf.String())
	assert.Equal(t, "Bundle", b.ResourceType)
	assert.Equal(t, "collection", b.Type)
	assert.Equal(t, "2026-10-14T12:00:00Z", b.Timestamp)

	var ids, kinds []string
	for _, e := range b.Entry {
		var r struct {
			ResourceType string `json:"resourceType"`
			ID           string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(e.Resource, &r))
		assert.Equal(t, snapBase+"/"+r.ResourceType+"/"+r.ID, e.FullURL)
		assert.Nil(t, e.Search, "a collection has no search mode")
		ids = append(ids, r.ID)
		kinds = append(kinds, r.ResourceType)
	}
	dev := fhir.DeviceID(snapUser, fhir.RecordDevice(hr1))
	assert.Equal(t, []string{"Patient", "Observation", "Observation", "Observation", "Observation", "Device", "Device"}, kinds)
	assert.Equal(t, snapUser.String(), ids[0])
	assert.Equal(t, []string{fhir.RecordID(hr1), fhir.RecordID(panel(3)), "sleep-6", fhir.RecordID(hr2)}, ids[1:5])
	assert.Contains(t, ids[5:], dev, "the ring's Device, once for its two samples")

	// The Patient is its id only: the tenant's reference to the user stays
	// in the tenant.
	assert.JSONEq(t, `{"resourceType":"Patient","id":"`+snapUser.String()+`","active":true}`, string(b.Entry[0].Resource))
}

func TestSnapshotEmpty(t *testing.T) {
	var buf bytes.Buffer
	snap, err := NewSnapshot(&buf, SnapshotOptions{User: snapUser, Base: snapBase, At: snapAt, Categories: []string{"heart"}})
	require.NoError(t, err)
	require.NoError(t, snap.Close())
	var b bundleJSON
	require.NoError(t, json.Unmarshal(buf.Bytes(), &b), buf.String())
	require.Len(t, b.Entry, 1, "the Patient alone")
	assert.Equal(t, 0, snap.Observations())
	assert.Empty(t, snap.Sources())
}

func TestSnapshotCap(t *testing.T) {
	var buf bytes.Buffer
	snap, err := NewSnapshot(&buf, SnapshotOptions{User: snapUser, Base: snapBase, At: snapAt,
		Categories: []string{"heart"}, Max: 2})
	require.NoError(t, err)
	require.NoError(t, snap.Add(quantity(1, "p", "heart_rate", 60, "count/min")))
	require.NoError(t, snap.Add(quantity(2, "p", "heart_rate", 60, "count/min")))
	assert.ErrorIs(t, snap.Add(quantity(3, "p", "heart_rate", 60, "count/min")), ErrTooLarge)
	assert.Equal(t, MaxObservations, 50000)
}

// A composite record carries figures of other categories: each one is
// shared on the terms of its own category, and a figure kept names its
// category in the share's sources.
func TestSnapshotFigures(t *testing.T) {
	sleep := sleepRecord(1, "invented_ring")
	sleep.Sleep.RestingHR, sleep.Sleep.HRVAvgMs = ptr(51.5), ptr(77.25)
	sleep.Sleep.RespiratoryRate, sleep.Sleep.SpO2Avg = ptr(14.5), ptr(96.5)
	workout := workoutRecord(2)
	workout.Workout.AvgHeartRate, workout.Workout.MaxHeartRate = ptr(141.0), ptr(171.0)
	workout.Workout.VO2Max, workout.Workout.DistanceM = ptr(48.5), ptr(10000.0)
	daily := dailyRecord(3)
	daily.Daily.RestingHR, daily.Daily.SleepScore = ptr(52.0), ptr(81.0)

	components := func(t *testing.T, categories []string, skip func(string, string) bool) (map[string][]string, []string) {
		t.Helper()
		var buf bytes.Buffer
		snap, err := NewSnapshot(&buf, SnapshotOptions{User: snapUser, Base: snapBase, At: snapAt,
			Categories: categories, Skip: skip})
		require.NoError(t, err)
		for _, rec := range []read.ObservationRecord{sleep, workout, daily} {
			require.NoError(t, snap.Add(rec))
		}
		require.NoError(t, snap.Close())
		var b bundleJSON
		require.NoError(t, json.Unmarshal(buf.Bytes(), &b), buf.String())
		out := map[string][]string{}
		for _, e := range b.Entry[1:] {
			var o fhir.Observation
			require.NoError(t, json.Unmarshal(e.Resource, &o))
			if o.ResourceType != "Observation" {
				continue
			}
			names := []string{}
			for _, c := range o.Component {
				for _, cd := range c.Code.Coding {
					if cd.System == fhir.ComponentSystem {
						names = append(names, cd.Code)
					}
				}
			}
			out[o.ID] = names
		}
		return out, snap.Sources()
	}

	t.Run("categories not shared", func(t *testing.T) {
		got, sources := components(t, []string{"sleep", "activity"}, nil)
		assert.Equal(t, []string{"step_count", "sleep_score"}, got["daily-3"], "the resting heart rate left out")
		assert.Equal(t, []string{"activity_type", "distance_m"}, got["workout-2"])
		assert.Empty(t, got["sleep-1"])
		assert.Equal(t, []string{"invented_ring/sleep", "invented_watch/activity", "invented_watch/sleep"}, sources)
	})
	t.Run("a category refused", func(t *testing.T) {
		got, sources := components(t, []string{"sleep", "activity", "heart", "respiratory"},
			func(provider, category string) bool { return provider == "invented_ring" && category == "heart" })
		assert.Equal(t, []string{"respiratory_rate", "spo2_avg"}, got["sleep-1"])
		assert.Equal(t, []string{"activity_type", "distance_m", "avg_heart_rate", "max_heart_rate", "vo2_max"}, got["workout-2"])
		assert.Equal(t, []string{"invented_ring/respiratory", "invented_ring/sleep", "invented_watch/activity",
			"invented_watch/heart", "invented_watch/respiratory", "invented_watch/sleep"}, sources)
	})
	assert.Equal(t, 51.5, *sleep.Sleep.RestingHR, "the caller's record stays as read")
}
