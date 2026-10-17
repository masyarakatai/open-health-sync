// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/soma-intelligence/open-health-sync/internal/api/read"
	"github.com/soma-intelligence/open-health-sync/internal/consent"
	"github.com/soma-intelligence/open-health-sync/internal/fhir"
)

// ErrTooLarge is returned by Snapshot.Add past the share's Observation cap.
var ErrTooLarge = errors.New("shl: the share holds too many observations")

// RecordCategory is the consent category of a record of the merged
// observation list: its quantity type's for a sample (a blood pressure panel
// is blood_pressure), sleep for a sleep session, activity for a workout or a
// daily summary, as consent.Filter files them.
func RecordCategory(r read.ObservationRecord) string {
	switch r.Kind {
	case read.KindSleep:
		return consent.Sleep
	case read.KindWorkout, read.KindDaily:
		return consent.Activity
	}
	return consent.Of(r.Sample.Type)
}

// RecordProvider is the provider a record came through.
func RecordProvider(r read.ObservationRecord) string {
	switch r.Kind {
	case read.KindSleep:
		return r.Sleep.Provider
	case read.KindWorkout:
		return r.Workout.Provider
	case read.KindDaily:
		return r.Daily.Provider
	}
	return r.Sample.Provider
}

// Filter is the merged observation list's filter for categories: the
// quantity samples of types (the user's) whose category is one of them,
// sleep sessions with sleep, workouts and daily summaries with activity.
// Types is never nil, so no category ever keeps every sample.
func Filter(categories, types []string) read.ObservationFilter {
	f := read.ObservationFilter{Quantities: true, Types: []string{}}
	for _, t := range types {
		if slices.Contains(categories, consent.Of(t)) {
			f.Types = append(f.Types, t)
		}
	}
	f.Sleep = slices.Contains(categories, consent.Sleep)
	f.Workouts = slices.Contains(categories, consent.Activity)
	f.Daily = f.Workouts
	return f
}

// SnapshotOptions say what a Snapshot writes.
type SnapshotOptions struct {
	// User is the person the share is about: the Patient, by id only.
	User uuid.UUID
	// Base is the FHIR base URL the entries' fullUrl hang from.
	Base string
	// At is the Bundle's timestamp, the time of the snapshot.
	At time.Time
	// Categories are the categories the share holds; a record of another
	// is left out.
	Categories []string
	// Skip, when set, leaves out the records of a provider and category it
	// returns true for (a consent refused, a purge pending).
	Skip func(provider, category string) bool
	// Max caps the Observations; 0 is MaxObservations.
	Max int
}

// Snapshot writes the Bundle of a share as its records come: a collection
// Bundle with the Patient first, the Observations in the order they are
// added, then the Devices they reference, each once. Only the Devices are
// held until the end; the rest goes straight to the writer.
type Snapshot struct {
	w       io.Writer
	enc     *json.Encoder
	o       SnapshotOptions
	n       int
	devices []fhir.Device
	seen    map[string]bool
	sources map[string]bool
}

// collectionEntry is one entry of a collection Bundle: no search mode.
type collectionEntry struct {
	FullURL  string `json:"fullUrl"`
	Resource any    `json:"resource"`
}

// NewSnapshot starts the Bundle on w.
func NewSnapshot(w io.Writer, o SnapshotOptions) (*Snapshot, error) {
	if o.Max <= 0 {
		o.Max = MaxObservations
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // as the FHIR routes write them
	s := &Snapshot{w: w, enc: enc, o: o, seen: map[string]bool{}, sources: map[string]bool{}}
	if _, err := io.WriteString(w, `{"resourceType":"Bundle","type":"collection","timestamp":"`+
		o.At.UTC().Format(time.RFC3339)+`","entry":[`+"\n"); err != nil {
		return nil, err
	}
	pt := fhir.FromUser(uuid.Nil, o.User, "") // no identifier: the tenant's reference stays in the tenant
	if err := enc.Encode(collectionEntry{FullURL: o.Base + "/Patient/" + pt.ID, Resource: pt}); err != nil {
		return nil, err
	}
	return s, nil
}

// allowed reports whether the share holds the category, from provider.
func (s *Snapshot) allowed(provider, category string) bool {
	return slices.Contains(s.o.Categories, category) && (s.o.Skip == nil || !s.o.Skip(provider, category))
}

// figure is one figure a composite record carries that belongs to a
// category of its own.
type figure struct {
	category string
	v        **float64
}

// figures lists the figures of r (a copy) filed under another category than
// the record's: a sleep session's or a daily summary's heart rate is heart
// data, a workout's VO2 max respiratory data, a daily summary's sleep score
// sleep data.
func figures(r *read.ObservationRecord) []figure {
	switch r.Kind {
	case read.KindSleep:
		return []figure{{consent.Heart, &r.Sleep.HRVAvgMs}, {consent.Heart, &r.Sleep.RestingHR},
			{consent.Respiratory, &r.Sleep.RespiratoryRate}, {consent.Respiratory, &r.Sleep.SpO2Avg}}
	case read.KindWorkout:
		return []figure{{consent.Heart, &r.Workout.AvgHeartRate}, {consent.Heart, &r.Workout.MaxHeartRate},
			{consent.Respiratory, &r.Workout.VO2Max}}
	case read.KindDaily:
		return []figure{{consent.Heart, &r.Daily.RestingHR}, {consent.Heart, &r.Daily.HRVAvgMs},
			{consent.Sleep, &r.Daily.SleepScore}}
	}
	return nil
}

// Add writes the Observation of r, unless its category is not shared or
// Skip leaves it out. A figure of r that belongs to another category is
// left out of the Observation on the same terms. Past Max it returns
// ErrTooLarge.
func (s *Snapshot) Add(r read.ObservationRecord) error {
	cat, prov := RecordCategory(r), RecordProvider(r)
	if !s.allowed(prov, cat) {
		return nil
	}
	if s.n >= s.o.Max {
		return ErrTooLarge
	}
	// The record is copied before a figure is removed: the caller's stays
	// as read.
	switch r.Kind {
	case read.KindSleep:
		v := *r.Sleep
		r.Sleep = &v
	case read.KindWorkout:
		v := *r.Workout
		r.Workout = &v
	case read.KindDaily:
		v := *r.Daily
		r.Daily = &v
	}
	kept := map[string]bool{}
	for _, f := range figures(&r) {
		switch {
		case *f.v == nil:
		case s.allowed(prov, f.category):
			kept[f.category] = true
		default:
			*f.v = nil
		}
	}
	o := fhir.FromRecord(s.o.User, r)
	if _, err := io.WriteString(s.w, ","); err != nil {
		return err
	}
	if err := s.enc.Encode(collectionEntry{FullURL: s.o.Base + "/Observation/" + o.ID, Resource: o}); err != nil {
		return err
	}
	s.n++
	s.sources[Source(prov, cat)] = true
	for c := range kept {
		s.sources[Source(prov, c)] = true
	}
	if d := fhir.RecordDevice(r); d.Known() {
		if dev := fhir.FromDevice(s.o.User, d); !s.seen[dev.ID] {
			s.seen[dev.ID] = true
			s.devices = append(s.devices, dev)
		}
	}
	return nil
}

// Close writes the Devices and ends the Bundle. It does not close the
// writer.
func (s *Snapshot) Close() error {
	for _, d := range s.devices {
		if _, err := io.WriteString(s.w, ","); err != nil {
			return err
		}
		if err := s.enc.Encode(collectionEntry{FullURL: s.o.Base + "/Device/" + d.ID, Resource: d}); err != nil {
			return err
		}
	}
	_, err := io.WriteString(s.w, "]}\n")
	return err
}

// Observations is how many Observations were written.
func (s *Snapshot) Observations() int { return s.n }

// Sources lists the provider/category pairs of the records written, sorted.
func (s *Snapshot) Sources() []string {
	out := make([]string, 0, len(s.sources))
	for k := range s.sources {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
