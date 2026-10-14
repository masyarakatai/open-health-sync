// SPDX-License-Identifier: AGPL-3.0-only

package fhir

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/api/read"
)

// ecgRecording is an invented Apple Watch recording, cut to 16 samples so
// the golden file stays readable: the real ones hold about 15360.
func ecgRecording() read.ECGRecordingDetail {
	const hz = 512.0
	offsets := make([]float64, 16)
	volts := []float64{12.5, 14.25, 20, 31.0000001, 58.75, 120.5, 310.125, 512, 260.5, 80, 15.5, -20.25, -35, -12.5, 0, 4.75}
	for i := range offsets {
		offsets[i] = float64(i) / hz
	}
	return read.ECGRecordingDetail{
		ECGRecording: read.ECGRecording{
			ID:                   11011,
			StartDate:            t0,
			EndDate:              ptr(t0.Add(30 * time.Second)),
			Classification:       ptr("sinus_rhythm"),
			ClassificationValue:  1,
			SymptomsStatus:       ptr("none"),
			AverageHeartRate:     ptr(64.0),
			SamplingFrequencyHz:  hz,
			NumberOfMeasurements: 15360,
			Metadata:             json.RawMessage(`{"timezone":"Europe/Paris"}`),
			Provenance:           read.Provenance{Provider: "apple_health", Source: ptr("apple_health"), IngestedAt: ingested},
		},
		Samples: read.ECGSamples{Count: len(volts), OffsetsS: offsets, VoltagesUV: volts},
	}
}

func TestECGRecording(t *testing.T) {
	o := FromECG(testUser, ecgRecording())
	assert.Equal(t, "ecg-11011", o.ID)
	assert.Equal(t, []Coding{{System: LOINC, Code: "11524-6"}, {System: RecordTypeSystem, Code: RecordECG}}, o.Code.Coding)
	assert.Empty(t, o.Category, "no HL7 category is claimed")
	assert.Nil(t, o.ValueQuantity, "the components carry the values")
	assert.Equal(t, &Period{Start: "2026-03-01T08:00:00Z", End: "2026-03-01T08:00:30Z"}, o.EffectivePeriod)
	require.Len(t, o.Component, 4)

	wave := o.Component[0]
	assert.Equal(t, Coding{System: MDC, Code: "131328", Display: "MDC_ECG_ELEC_POTL"}, wave.Code.Coding[0])
	require.NotNil(t, wave.ValueSampledData)
	sd := wave.ValueSampledData
	assert.Equal(t, 1.953125, sd.Period, "1000 ms / 512 Hz")
	assert.Equal(t, 1, sd.Dimensions)
	assert.Equal(t, "uV", sd.Origin.Code)
	assert.Equal(t, UCUM, sd.Origin.System)
	assert.Equal(t, 0.0, *sd.Origin.Value)
	assert.Equal(t, "12.5 14.25 20 31 58.75 120.5 310.125 512 260.5 80 15.5 -20.25 -35 -12.5 0 4.75", sd.Data)

	assert.Equal(t, "8867-4", o.Component[1].Code.Coding[0].Code)
	assert.Equal(t, "/min", o.Component[1].ValueQuantity.Code)

	class := o.Component[2]
	assert.Equal(t, "device_classification", class.Code.Coding[0].Code)
	assert.Equal(t, "classification by the recording device", class.Code.Text)
	assert.Equal(t, []Coding{{System: HealthKitECGClassificationSystem, Code: "sinus_rhythm"}}, class.ValueCodeableConcept.Coding)
	assert.Equal(t, "sinus rhythm", class.ValueCodeableConcept.Text)

	assert.Equal(t, []Coding{{System: ECGSymptomsStatusSystem, Code: "none"}}, o.Component[3].ValueCodeableConcept.Coding)
	assert.Nil(t, o.Device, "the row names no device")
}

func TestECGWithings(t *testing.T) {
	e := ecgRecording()
	e.Provider, e.Source = "withings", ptr("withings")
	e.Classification, e.ClassificationValue = ptr("negative"), 0
	e.SymptomsStatus = ptr("not_set")
	e.SamplingFrequencyHz = 300
	o := FromECG(testUser, e)
	require.Len(t, o.Component, 3, "no symptoms component when not set")
	assert.InDelta(t, 3.3333, o.Component[0].ValueSampledData.Period, 0.0001)
	assert.Equal(t, []Coding{{System: AFibClassificationSystem, Code: "negative"}}, o.Component[2].ValueCodeableConcept.Coding,
		"the codes of the Withings AFib quantity sample")
}

func TestECGWithoutWhatIsUnknown(t *testing.T) {
	e := ecgRecording()
	e.Classification, e.ClassificationValue = nil, 42
	e.AverageHeartRate = nil
	e.EndDate = nil
	o := FromECG(testUser, e)
	require.Len(t, o.Component, 2, "no classification label, no heart rate")
	assert.Equal(t, "2026-03-01T08:00:00Z", o.EffectiveDateTime)

	e = ecgRecording()
	e.Classification = ptr(read.ECGClassificationNotSet)
	o = FromECG(testUser, e)
	for _, c := range o.Component {
		assert.NotEqual(t, "device_classification", c.Code.Coding[0].Code, "not set is no classification")
	}

	// A frequency the row does not know: the period comes from the offsets.
	e = ecgRecording()
	e.SamplingFrequencyHz = 0
	o = FromECG(testUser, e)
	assert.InDelta(t, 1.953125, o.Component[0].ValueSampledData.Period, 1e-9)

	// No frequency and a single sample: no period, no waveform.
	e.Samples = read.ECGSamples{Count: 1, OffsetsS: []float64{0}, VoltagesUV: []float64{1}}
	o = FromECG(testUser, e)
	assert.Nil(t, o.Component[0].ValueSampledData)

	// No sample stored.
	e = ecgRecording()
	e.Samples = read.ECGSamples{OffsetsS: []float64{}, VoltagesUV: []float64{}}
	o = FromECG(testUser, e)
	for _, c := range o.Component {
		assert.Nil(t, c.ValueSampledData)
	}
}

func TestSampledData(t *testing.T) {
	assert.Equal(t, "0 1.235 -1.235 E E E 1000000", sampledData([]float64{-0.0001, 1.23456, -1.23456,
		math.NaN(), math.Inf(1), math.Inf(-1), 1e6}))
	assert.Empty(t, sampledData(nil))
	// No exponent, whatever the magnitude.
	assert.False(t, strings.ContainsAny(sampledData([]float64{1e-9, 1e21}), "eE"))
}
