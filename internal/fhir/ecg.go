// SPDX-License-Identifier: AGPL-3.0-only

package fhir

import (
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/soma-intelligence/open-health-sync/internal/api/read"
)

// Codes of the ECG recording mapping (docs/specs/ecg-read.md lists where
// each one was checked).
const (
	// LOINCECGStudy is LOINC 11524-6, EKG study: the recording as a whole.
	LOINCECGStudy = "11524-6"
	// MDC is the IEEE 11073-10101 nomenclature, in the URI form the HL7
	// Point-of-Care Device IG uses.
	MDC = "urn:iso:std:iso:11073:10101"
	// MDCECGPotential is MDC_ECG_ELEC_POTL, the ECG electric potential: the
	// waveform. No lead is named, since the stored rows do not say which.
	MDCECGPotential = "131328"
	loincHeartRate  = "8867-4"

	// RecordECG is the record type of an ECG recording.
	RecordECG = "ecg_recording"
	// ECGIDPrefix starts the id of the Observation of an ECG recording.
	ECGIDPrefix = "ecg-"

	// HealthKitECGClassificationSystem codes the classification Apple's
	// HealthKit gives a recording (sinus_rhythm, inconclusive_other...).
	HealthKitECGClassificationSystem = Namespace + "/CodeSystem/healthkit-ecg-classification"
	// ECGSymptomsStatusSystem codes whether the person recorded symptoms
	// with the recording: none or present.
	ECGSymptomsStatusSystem = Namespace + "/CodeSystem/ecg-symptoms-status"
)

// ecgClassificationSystems are the code systems of the device
// classification, per provider.
var ecgClassificationSystems = map[string]string{
	"apple_health": HealthKitECGClassificationSystem,
	"withings":     AFibClassificationSystem,
}

// FromECG maps one ECG recording to an Observation: the waveform as a
// SampledData component in microvolts, the average heart rate, and the
// classification the recording device made, as the device's.
func FromECG(user uuid.UUID, e read.ECGRecordingDetail) Observation {
	o := newObservation(user, ECGIDPrefix+strconv.FormatInt(e.ID, 10), e.Provenance, nil)
	o.Code = recordCode(LOINCECGStudy, RecordECG, "ECG recording")
	o.effective(e.StartDate, e.EndDate)
	if c, ok := ecgWaveform(e); ok {
		o.Component = append(o.Component, c)
	}
	o.Component = append(o.Component, components(field{name: "average_heart_rate", loinc: loincHeartRate,
		text: "average heart rate", unit: "bpm", ucum: "/min", v: e.AverageHeartRate})...)
	if c := deref(e.Classification); c != "" && c != read.ECGClassificationNotSet {
		if system, ok := ecgClassificationSystems[e.Provider]; ok {
			o.Component = append(o.Component, codedComponent("device_classification",
				"classification by the recording device", system, c))
		}
	}
	if s := deref(e.SymptomsStatus); s == "none" || s == "present" {
		o.Component = append(o.Component, codedComponent("symptoms_status",
			"symptoms recorded with the ECG", ECGSymptomsStatusSystem, s))
	}
	return o
}

// codedComponent is a component whose value is code in system.
func codedComponent(name, text, system, code string) Component {
	return Component{
		Code: CodeableConcept{Coding: []Coding{{System: ComponentSystem, Code: name}}, Text: text},
		ValueCodeableConcept: &CodeableConcept{
			Coding: []Coding{{System: system, Code: code}},
			Text:   strings.ReplaceAll(code, "_", " "),
		},
	}
}

// SampledData is a FHIR R4 SampledData.
type SampledData struct {
	Origin     Quantity `json:"origin"`
	Period     float64  `json:"period"`
	Dimensions int      `json:"dimensions"`
	Data       string   `json:"data,omitempty"`
}

// ecgWaveform is the voltage component: the samples in offset order, one
// period (milliseconds) apart, from the sampling frequency or, when it is
// unknown, from the first two offsets. ok is false when no period can be
// known or no sample is stored.
func ecgWaveform(e read.ECGRecordingDetail) (Component, bool) {
	v := e.Samples.VoltagesUV
	if len(v) == 0 {
		return Component{}, false
	}
	period := 0.0
	switch off := e.Samples.OffsetsS; {
	case e.SamplingFrequencyHz > 0:
		period = 1000 / e.SamplingFrequencyHz
	case len(off) > 1:
		period = (off[1] - off[0]) * 1000
	}
	if !(period > 0) || math.IsInf(period, 0) {
		return Component{}, false
	}
	zero := 0.0
	return Component{
		Code: CodeableConcept{
			Coding: []Coding{{System: MDC, Code: MDCECGPotential, Display: "MDC_ECG_ELEC_POTL"}, {System: ComponentSystem, Code: "voltage"}},
			Text:   "ECG voltage, single lead",
		},
		ValueSampledData: &SampledData{
			Origin:     Quantity{Value: &zero, Unit: "uV", System: UCUM, Code: "uV"},
			Period:     period,
			Dimensions: 1,
			Data:       sampledData(v),
		},
	}, true
}

// sampledData writes voltages as SampledData.data: decimals rounded to
// 0.001 microvolt, separated by a space, E (error, no value) for a value
// that is not a finite number.
func sampledData(v []float64) string {
	var b strings.Builder
	b.Grow(len(v) * 9)
	for i, x := range v {
		if i > 0 {
			b.WriteByte(' ')
		}
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b.WriteByte('E')
			continue
		}
		r := math.Round(x*1000) / 1000
		if r == 0 {
			r = 0 // no "-0"
		}
		b.WriteString(strconv.FormatFloat(r, 'f', -1, 64))
	}
	return b.String()
}
