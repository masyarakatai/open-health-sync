// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetECGRecordings(t *testing.T) {
	e := newEnv(t)
	start := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	var id int64
	require.NoError(t, e.pool.QueryRow(context.Background(), `INSERT INTO ohs_data.ecg_recordings
		(tenant_id, user_id, provider, source_slug, start_date, end_date, classification, symptoms_status,
		 average_heart_rate, sampling_frequency, number_of_measurements, content_hash)
		SELECT tenant_id, id, 'apple_health', 'apple_health', $2, $3, 1, 1, 62, 512, 4, 'ecg-1'
		FROM ohs_core.end_users WHERE id = $1 RETURNING id`, e.userA, start, start.Add(30*time.Second)).Scan(&id))
	for i, v := range []float64{-12.5, 40, 310.25, 8} {
		e.exec(`INSERT INTO ohs_data.ecg_samples
			(tenant_id, user_id, ecg_id, ts, time_since_sample_start, voltage_uv, content_hash)
			SELECT tenant_id, id, $2::bigint, $3::timestamptz + make_interval(secs => $4::float8), $4::float8, $5, 'ecg-1-' || $6::int::text
			FROM ohs_core.end_users WHERE id = $1`, e.userA, id, start, float64(i)/512, v, i)
	}
	cs := Connect(t, e.srv.URL+"/mcp", e.keyA)
	u := e.userA.String()

	// The list: the REST list, metadata only.
	r := get(t, cs, "get_ecg_recordings", map[string]any{"user_id": u})
	assert.False(t, r.Truncated)
	assert.Equal(t, 1, count(t, r))
	assert.JSONEq(t, string(e.rest("/v1/users/"+u+"/ecg", e.keyA)), string(r.Records))
	assert.NotContains(t, string(r.Records), "voltages_uv")
	r = get(t, cs, "get_ecg_recordings", map[string]any{"user_id": u, "start": "2026-03-02"})
	assert.Equal(t, 0, count(t, r))

	// One recording: a summary of its samples, not the samples.
	text, isErr := call(t, cs, "get_ecg_recordings", map[string]any{"user_id": u, "recording_id": id})
	require.False(t, isErr, text)
	assert.NotContains(t, text, "voltages_uv")
	var s struct {
		ID             int64   `json:"id"`
		Classification string  `json:"classification"`
		StoredSamples  int     `json:"stored_samples"`
		FirstOffsetS   float64 `json:"first_offset_s"`
		LastOffsetS    float64 `json:"last_offset_s"`
		MinVoltageUV   float64 `json:"min_voltage_uv"`
		MaxVoltageUV   float64 `json:"max_voltage_uv"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &s), text)
	assert.Equal(t, id, s.ID)
	assert.Equal(t, "sinus_rhythm", s.Classification)
	assert.Equal(t, 4, s.StoredSamples)
	assert.Equal(t, 0.0, s.FirstOffsetS)
	assert.InDelta(t, 3.0/512, s.LastOffsetS, 1e-12)
	assert.Equal(t, -12.5, s.MinVoltageUV)
	assert.Equal(t, 310.25, s.MaxVoltageUV)

	// Another tenant's key and an unknown recording read alike.
	for _, args := range []map[string]any{
		{"user_id": u, "recording_id": 999999},
		{"user_id": e.userB.String(), "recording_id": id},
	} {
		msg, isErr := call(t, cs, "get_ecg_recordings", args)
		assert.True(t, isErr)
		assert.Equal(t, "ECG recording not found", msg)
	}
	other := Connect(t, e.srv.URL+"/mcp", e.keyB)
	msg, isErr := call(t, other, "get_ecg_recordings", map[string]any{"user_id": u, "recording_id": id})
	assert.True(t, isErr)
	assert.NotContains(t, msg, "sinus")

	// A personal token reads its own user's recordings without user_id.
	mine := Connect(t, e.srv.URL+"/mcp", e.token(e.keyA, e.userA, "read"))
	r = get(t, mine, "get_ecg_recordings", nil)
	assert.Equal(t, 1, count(t, r))
}
