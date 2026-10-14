// SPDX-License-Identifier: AGPL-3.0-only

package read

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestECGLabels(t *testing.T) {
	assert.Equal(t, "sinus_rhythm", ECGClassification("apple_health", 1))
	assert.Equal(t, "inconclusive_poor_reading", ECGClassification("apple_health", 5))
	assert.Equal(t, "unrecognized", ECGClassification("apple_health", 100))
	assert.Equal(t, ECGClassificationNotSet, ECGClassification("apple_health", 0))
	assert.Empty(t, ECGClassification("apple_health", 7), "not a HealthKit value")
	assert.Equal(t, "negative", ECGClassification("withings", 0))
	assert.Equal(t, "inconclusive", ECGClassification("withings", 2))
	assert.Empty(t, ECGClassification("withings", 100))
	assert.Empty(t, ECGClassification("oura", 1), "a provider with no vocabulary")

	assert.Equal(t, "none", ECGSymptomsStatus(1))
	assert.Equal(t, "present", ECGSymptomsStatus(2))
	assert.Empty(t, ECGSymptomsStatus(3))

	r := ecgRow{rec: ECGRecording{Provenance: Provenance{Provider: "apple_health"}, ClassificationValue: 9}, symptoms: 9}
	got := r.finish()
	assert.Nil(t, got.Classification, "an unknown value has no label")
	assert.Nil(t, got.SymptomsStatus)
	r.rec.ClassificationValue, r.symptoms = 2, 0
	got = r.finish()
	assert.Equal(t, "atrial_fibrillation", *got.Classification)
	assert.Equal(t, "not_set", *got.SymptomsStatus)
}

func TestECGSpan(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	end := t0.Add(30 * time.Second)
	from, to := ecgSpan([]ECGRecording{{StartDate: t0, EndDate: &end, SamplingFrequencyHz: 512, NumberOfMeasurements: 15360}})
	assert.Equal(t, t0, from)
	assert.Equal(t, t0.Add(30*time.Second+time.Hour), to)

	// No end and no frequency: the count at 1 Hz bounds the offsets.
	from, to = ecgSpan([]ECGRecording{{StartDate: t0, NumberOfMeasurements: 9000}})
	assert.Equal(t, t0, from)
	assert.Equal(t, t0.Add(9000*time.Second+time.Hour), to)

	// More samples than the duration holds: the count wins.
	from, to = ecgSpan([]ECGRecording{{StartDate: t0, EndDate: &end, SamplingFrequencyHz: 300, NumberOfMeasurements: 18000}})
	assert.Equal(t, t0, from)
	assert.Equal(t, t0.Add(60*time.Second+time.Hour), to)

	// Several recordings: from the earliest start to the latest end.
	t1 := t0.Add(48 * time.Hour)
	from, to = ecgSpan([]ECGRecording{
		{StartDate: t1, SamplingFrequencyHz: 512, NumberOfMeasurements: 512},
		{StartDate: t0, SamplingFrequencyHz: 512, NumberOfMeasurements: 512},
	})
	assert.Equal(t, t0, from)
	assert.Equal(t, t1.Add(time.Second+time.Hour), to)
}

func TestCutPage(t *testing.T) {
	defer func(v int) { MaxECGPerPage = v }(MaxECGPerPage)
	MaxECGPerPage = 2
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	key := func(i int, k Kind) obsKey {
		return obsKey{kind: k, t: t0.Add(time.Duration(i) * time.Minute), id: int64(i)}
	}

	// Fewer keys than the limit: the last page.
	keys := []obsKey{key(1, KindQuantity), key(2, KindECG)}
	got, next := cutPage(keys, 5)
	assert.Equal(t, keys, got)
	assert.Empty(t, next)

	// One more than the limit: the page ends at the limit.
	keys = []obsKey{key(1, KindQuantity), key(2, KindSleep), key(3, KindQuantity)}
	got, next = cutPage(keys, 2)
	assert.Equal(t, keys[:2], got)
	assert.Equal(t, encodeObsCursor(keys[1].t, KindSleep, 2), next)

	// The second ECG ends the page when records follow it.
	keys = []obsKey{key(1, KindECG), key(2, KindQuantity), key(3, KindECG), key(4, KindQuantity), key(5, KindECG)}
	got, next = cutPage(keys, 10)
	assert.Equal(t, keys[:3], got)
	assert.Equal(t, encodeObsCursor(keys[2].t, KindECG, 3), next)

	// The second ECG is the last key: nothing follows, no cursor.
	keys = []obsKey{key(1, KindQuantity), key(2, KindECG), key(3, KindECG)}
	got, next = cutPage(keys, 10)
	assert.Equal(t, keys, got)
	assert.Empty(t, next)

	// The second ECG is the last of the page and one more key was read.
	keys = []obsKey{key(1, KindECG), key(2, KindECG), key(3, KindQuantity)}
	got, next = cutPage(keys, 2)
	assert.Equal(t, keys[:2], got)
	assert.Equal(t, encodeObsCursor(keys[1].t, KindECG, 2), next)
}

func TestECGFilter(t *testing.T) {
	ecg := ObservationFilter{ECG: true}
	assert.False(t, ecg.Empty())
	assert.Equal(t, ecg, AllObservations().And(ecg))
	assert.True(t, ecg.And(ObservationFilter{Sleep: true}).Empty())
	assert.True(t, ecg.And(ObservationFilter{Quantities: true}).Empty())
}
