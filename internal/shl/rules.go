// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/soma-intelligence/open-health-sync/internal/consent"
)

// Limits of a share.
const (
	// MaxLifetime is how far ahead a share may expire.
	MaxLifetime = 365 * 24 * time.Hour
	// MaxObservations is how many Observations one share may hold.
	MaxObservations = 50000
)

// Shareable are the consent categories a share may hold, in documentation
// order: those with records in the FHIR view. The others (location, ecg,
// nutrition, reproductive, mental) have none there yet.
var Shareable = []string{consent.Activity, consent.Heart, consent.BloodPressure, consent.Glucose,
	consent.Respiratory, consent.Body, consent.Sleep, consent.Other}

// CheckExpiry refuses an expiry that is not after now or is more than
// MaxLifetime ahead.
func CheckExpiry(now, exp time.Time) error {
	switch {
	case exp.IsZero():
		return errors.New("expires_at is required")
	case !exp.After(now):
		return errors.New("expires_at must be in the future")
	case exp.Sub(now) > MaxLifetime:
		return errors.New("expires_at must be at most 365 days ahead")
	}
	return nil
}

// CheckWindow refuses a time window without both bounds or whose end is not
// after its start.
func CheckWindow(start, end time.Time) error {
	switch {
	case start.IsZero() || end.IsZero():
		return errors.New("start and end are required")
	case !end.After(start):
		return errors.New("end must be after start")
	}
	return nil
}

// CheckCategories returns cs in documentation order, each once, or an
// error naming the first category that cannot be shared.
func CheckCategories(cs []string) ([]string, error) {
	if len(cs) == 0 {
		return nil, errors.New("categories is required: one or more of " + strings.Join(Shareable, ", "))
	}
	for _, c := range cs {
		switch {
		case slices.Contains(Shareable, c):
		case consent.Known(c):
			return nil, fmt.Errorf("category %s cannot be shared yet: use %s", strconv.Quote(c), strings.Join(Shareable, ", "))
		default:
			return nil, fmt.Errorf("unknown category %s: use %s", strconv.Quote(c), strings.Join(Shareable, ", "))
		}
	}
	var out []string
	for _, c := range Shareable {
		if slices.Contains(cs, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// CheckLabel refuses a label longer than MaxLabel characters, not UTF-8, or
// holding control characters.
func CheckLabel(s string) error {
	if !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return errors.New("label must be one line of text")
	}
	if utf8.RuneCountInString(s) > MaxLabel {
		return fmt.Errorf("label must be at most %d characters", MaxLabel)
	}
	return nil
}

// Source names the records of one category from one provider, as a share
// lists what it holds: "provider/category".
func Source(provider, category string) string { return provider + "/" + category }
