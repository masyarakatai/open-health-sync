// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckExpiry(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, ok := range []time.Time{now.Add(time.Second), now.Add(time.Hour), now.Add(MaxLifetime)} {
		assert.NoError(t, CheckExpiry(now, ok), ok)
	}
	for _, bad := range []time.Time{{}, now, now.Add(-time.Second), now.Add(MaxLifetime + time.Second)} {
		assert.Error(t, CheckExpiry(now, bad), bad)
	}
	assert.Equal(t, 365*24*time.Hour, MaxLifetime)
}

func TestCheckWindow(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	assert.NoError(t, CheckWindow(start, start.Add(time.Second)))
	assert.Error(t, CheckWindow(start, start))
	assert.Error(t, CheckWindow(start, start.Add(-time.Hour)))
	assert.Error(t, CheckWindow(time.Time{}, start))
	assert.Error(t, CheckWindow(start, time.Time{}))
}

func TestCheckCategories(t *testing.T) {
	got, err := CheckCategories([]string{"sleep", "heart", "sleep", "activity"})
	require.NoError(t, err)
	assert.Equal(t, []string{"activity", "heart", "sleep"}, got, "documentation order, each once")

	got, err = CheckCategories(Shareable)
	require.NoError(t, err)
	assert.Equal(t, Shareable, got)
	assert.Equal(t, []string{"activity", "heart", "blood_pressure", "glucose", "respiratory", "body", "sleep", "other"},
		Shareable)

	for name, bad := range map[string][]string{
		"none":         nil,
		"empty":        {},
		"unknown":      {"heart", "invented"},
		"ecg":          {"ecg"},
		"location":     {"location"},
		"nutrition":    {"nutrition"},
		"reproductive": {"reproductive"},
		"mental":       {"mental"},
		"blank":        {""},
	} {
		_, err := CheckCategories(bad)
		assert.Error(t, err, name)
	}
	// The message names the category, so the caller knows which one.
	_, err = CheckCategories([]string{"heart", "ecg"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"ecg"`)
}

func TestCheckLabel(t *testing.T) {
	for _, ok := range []string{"", "For my coach", strings.Repeat("é", MaxLabel)} {
		assert.NoError(t, CheckLabel(ok), ok)
	}
	for _, bad := range []string{strings.Repeat("a", MaxLabel+1), "two\nlines", "tab\there", "bad \xff"} {
		assert.Error(t, CheckLabel(bad), bad)
	}
}

func TestSource(t *testing.T) {
	assert.Equal(t, "invented_ring/heart", Source("invented_ring", "heart"))
}
