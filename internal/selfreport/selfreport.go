// SPDX-License-Identifier: AGPL-3.0-only

// Package selfreport defines a self report: a person's own rating (how they
// feel, their mood, stress, energy, sleep quality, muscle soreness, the
// effort of a session, pain), a whole number on a fixed scale. The API route
// POST /v1/users/{id}/self-reports checks reports with it and stores them as
// one NDJSON part of Line; the self report connector (connectors/selfreport)
// maps each stored line back to one quantity sample with Sample.
//
// A report carries an id. Sending a report again with the same id replaces
// the stored rating (latest wins), also when its start moved. The line format
// is documented in docs/self-reports.md.
package selfreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// Slug is the provider and the source slug of every self report sample.
const Slug = "self_report"

// DataType is the X-OHS-Type of a self report part.
const DataType = "self_report"

// MaxLineBytes is the longest line a part may hold: a report is a few
// hundred bytes.
const MaxLineBytes = 4096

// MaxTimezone is the longest time zone name a report may carry.
const MaxTimezone = 64

// TypePrefix is what a kind becomes a quantity type with.
const TypePrefix = "reported_"

// Kinds lists the report kinds, in documentation order.
func Kinds() []string {
	out := make([]string, 0, len(model.SelfReportScales))
	for _, s := range model.SelfReportScales {
		out = append(out, strings.TrimPrefix(s.Type, TypePrefix))
	}
	return out
}

// QuantityType is the quantity type a kind is stored as, and false for a
// kind that does not exist.
func QuantityType(kind string) (string, bool) {
	qt := TypePrefix + kind
	if _, ok := model.SelfReportScale(qt); !ok || kind == "" {
		return "", false
	}
	return qt, true
}

// The range a report's times must fall in.
var (
	minTime = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	maxTime = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
)

// Report is one rating, as an upload line carries it.
type Report struct {
	// ID identifies the report: sending it again replaces the stored rating.
	ID string `json:"id"`
	// Kind is one of Kinds.
	Kind string `json:"kind"`
	// Value is a whole number on the kind's scale.
	Value float64 `json:"value"`
	// StartAt is when the rating applies; EndAt, when set, ends the period it
	// rates (a training session).
	StartAt time.Time  `json:"start_at"`
	EndAt   *time.Time `json:"end_at,omitempty"`
	// Timezone is the IANA zone the person was in, for their local day.
	Timezone string `json:"timezone,omitempty"`
}

// InvalidError is a report that cannot be stored. Msg names the field and
// never quotes its value.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return &InvalidError{Msg: fmt.Sprintf(format, args...)}
}

// Check validates r and normalizes it: the id in its canonical form, times
// in UTC to the microsecond. An empty id is refused; the API gives one before
// it calls Check.
func (r *Report) Check() error {
	id, err := uuid.Parse(r.ID)
	if r.ID == "" || err != nil {
		return invalid("id must be a UUID")
	}
	r.ID = id.String()
	qt, ok := QuantityType(r.Kind)
	if !ok {
		return invalid("kind must be one of %s", strings.Join(Kinds(), ", "))
	}
	sc, _ := model.SelfReportScale(qt)
	if math.IsNaN(r.Value) || math.Trunc(r.Value) != r.Value || r.Value < float64(sc.Min) || r.Value > float64(sc.Max) {
		return invalid("value must be a whole number from %d to %d for kind %s", sc.Min, sc.Max, r.Kind)
	}
	if r.StartAt.IsZero() {
		return invalid("start_at is required")
	}
	r.StartAt = r.StartAt.UTC().Truncate(time.Microsecond)
	if r.StartAt.Before(minTime) || !r.StartAt.Before(maxTime) {
		return invalid("start_at must be between 1900 and 2200")
	}
	if r.EndAt != nil {
		end := r.EndAt.UTC().Truncate(time.Microsecond)
		if !end.After(r.StartAt) || !end.Before(maxTime) {
			return invalid("end_at must be after start_at and before 2200")
		}
		r.EndAt = &end
	}
	if r.Timezone != "" {
		if len(r.Timezone) > MaxTimezone || r.Timezone == "Local" {
			return invalid("timezone must be an IANA time zone name, for example Europe/Paris")
		}
		if _, err := time.LoadLocation(r.Timezone); err != nil {
			return invalid("timezone must be an IANA time zone name, for example Europe/Paris")
		}
	}
	return nil
}

// Decode reads one line strictly: an unknown member is a mistake worth
// refusing (a misspelt value would otherwise be lost).
func Decode(line []byte) (Report, error) {
	var r Report
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Report{}, errors.New("is not a report object")
	}
	if dec.More() {
		return Report{}, errors.New("holds more than one JSON value")
	}
	if err := r.Check(); err != nil {
		return Report{}, err
	}
	return r, nil
}

// Sample is the quantity sample of a checked report of user.
func Sample(user string, r Report) model.QuantitySample {
	qt, _ := QuantityType(r.Kind)
	end := r.StartAt
	if r.EndAt != nil {
		end = *r.EndAt
	}
	entered := true
	s := model.QuantitySample{
		UserID:          user,
		Provider:        Slug,
		SourceSlug:      Slug,
		SourceName:      "Self report",
		ExternalID:      r.ID,
		QuantityType:    qt,
		Value:           r.Value,
		Unit:            "score",
		IsPrimarySource: true,
		WasUserEntered:  &entered,
		StartDate:       r.StartAt,
		EndDate:         end,
		Metadata:        map[string]any{},
		// A correction carries the same id: its value replaces the stored
		// one, and a moved start replaces the sample at the old start.
		Replaceable:    true,
		SupersedeMoved: true,
	}
	if r.Timezone != "" {
		s.Metadata["timezone"] = r.Timezone
	}
	s.ContentHash = model.ComputeContentHash(map[string]string{
		"user_id":       user,
		"provider":      Slug,
		"quantity_type": qt,
		"external_id":   r.ID,
		"start_date":    r.StartAt.UTC().Format(time.RFC3339Nano),
	})
	return s
}

// Line is the NDJSON line of a checked report, newline included.
func Line(r Report) ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("self_report: line: %w", err)
	}
	return append(b, '\n'), nil
}
