// SPDX-License-Identifier: AGPL-3.0-only

package xiaomi

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
	"github.com/soma-intelligence/open-health-sync/internal/model"
)

const testUser = "00000000-0000-0000-0000-000000000001"

// lines reads testdata/upload.ndjson, the upload `ohs import mi-fitness`
// makes of the invented copy in export/testdata (regenerate it with
// go test ./connectors/xiaomi/export -update), keyed by table/key, with a
// second line of a key under table/key#2.
func lines(t *testing.T) map[string]map[string]any {
	t.Helper()
	f, err := os.Open("testdata/upload.ndjson")
	require.NoError(t, err)
	defer f.Close()
	out := map[string]map[string]any{}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, MaxLineBytes)
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m))
		k := m["table"].(string) + "/" + m["key"].(string)
		if _, dup := out[k]; dup {
			k += "#2"
		}
		out[k] = m
	}
	require.NoError(t, sc.Err())
	return out
}

func mapOne(t *testing.T, fields map[string]any) model.MapResult {
	t.Helper()
	res, err := New().Map(connector.RawRecord{UserID: testUser, Provider: Slug, Fields: fields})
	require.NoError(t, err)
	return res
}

func one(t *testing.T, res model.MapResult) model.QuantitySample {
	t.Helper()
	require.Len(t, res.QuantitySamples, 1)
	return res.QuantitySamples[0]
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseUpload(t *testing.T) {
	c := New()
	f, err := os.Open("testdata/upload.ndjson")
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, c.ParseUpload(connector.UploadMeta{}, f, nil))

	for name, body := range map[string]string{
		"not json": "{\"table\":\"fitness_data\",\"key\":\"steps\"}\nnope\n",
		"array":    "[1,2]\n",
		"no table": "{\"key\":\"steps\"}\n",
		"no key":   "{\"table\":\"fitness_data\"}\n",
		"empty":    "{\"table\":\"\",\"key\":\"steps\"}\n",
	} {
		err := c.ParseUpload(connector.UploadMeta{}, strings.NewReader(body), nil)
		require.ErrorIs(t, err, ErrInvalidLine, name)
	}
	require.ErrorIs(t, c.ParseUpload(connector.UploadMeta{}, strings.NewReader("\n  \n"), nil), ErrNoLines)
}

func TestMapHeartRate(t *testing.T) {
	l := lines(t)
	s := one(t, mapOne(t, l["fitness_data/heart_rate"]))
	require.Equal(t, model.QTHeartRate, s.QuantityType)
	require.Equal(t, 59.0, s.Value)
	require.Equal(t, "bpm", s.Unit)
	require.Equal(t, at("2026-03-08T00:04:00Z"), s.StartDate)
	require.Equal(t, s.StartDate, s.EndDate)
	require.Equal(t, Slug, s.Provider)
	require.Equal(t, Slug, s.SourceSlug)
	require.Equal(t, "Mi Fitness", s.SourceName)
	require.Equal(t, "band.example.m1", s.DeviceModel)
	require.Equal(t, "Xiaomi band.example.m1", s.DeviceID)
	require.Equal(t, "2000000001_heart_rate_1772928240", s.ExternalID)
	require.Equal(t, "Xiaomi Smart Band 8", s.Metadata["device_name"])
	require.True(t, s.Replaceable)
	require.NotEmpty(t, s.ContentHash)
	_, hasZone := s.Metadata["timezone"]
	require.False(t, hasZone, "this reading carries no timezone")

	// A placeholder zero is not a reading.
	require.Empty(t, mapOne(t, l["fitness_data/heart_rate#2"]).QuantitySamples)

	spot := one(t, mapOne(t, l["fitness_data/single_heart_rate"]))
	require.Equal(t, model.QTHeartRate, spot.QuantityType)
	require.Equal(t, 71.0, spot.Value)
	require.Equal(t, "Etc/GMT-1", spot.Metadata["timezone"], "timezone 4 is four quarters of an hour east")
	require.Equal(t, 60, spot.Metadata["utc_offset_minutes"])
}

func TestMapInstants(t *testing.T) {
	l := lines(t)
	for key, want := range map[string]struct {
		qt, unit string
		v        float64
		start    string
	}{
		"resting_heart_rate": {model.QTRestingHeartRate, "bpm", 52, "2026-03-08T00:00:00Z"},
		"spo2":               {model.QTOxygenSaturation, "percent", 97, "2026-03-08T02:00:00Z"},
		"vo2_max":            {"vo2_max", "mL/kg/min", 46, "2026-03-08T07:00:00Z"},
	} {
		s := one(t, mapOne(t, l["fitness_data/"+key]))
		require.Equal(t, want.qt, s.QuantityType, key)
		require.Equal(t, want.unit, s.Unit, key)
		require.Equal(t, want.v, s.Value, key)
		require.Equal(t, at(want.start), s.StartDate, key)
	}
}

func TestMapStepsAndCalories(t *testing.T) {
	l := lines(t)
	res := mapOne(t, l["fitness_data/steps"])
	require.Len(t, res.QuantitySamples, 2, "steps and distance; the row's calories stay raw")
	st, dist := res.QuantitySamples[0], res.QuantitySamples[1]
	require.Equal(t, model.QTStepCount, st.QuantityType)
	require.Equal(t, 84.0, st.Value)
	require.Equal(t, at("2026-03-08T09:00:00Z"), st.StartDate)
	require.Equal(t, at("2026-03-08T09:01:00Z"), st.EndDate)
	require.Equal(t, model.QTDistanceWalkingRunning, dist.QuantityType)
	require.Equal(t, 61.0, dist.Value)
	require.Equal(t, "m", dist.Unit)
	require.NotEqual(t, st.ContentHash, dist.ContentHash)

	kcal := one(t, mapOne(t, l["fitness_data/calories"]))
	require.Equal(t, model.QTActiveEnergyBurned, kcal.QuantityType)
	require.Equal(t, 3.5, kcal.Value)
	require.Equal(t, "kcal", kcal.Unit)
}

func TestMapWeight(t *testing.T) {
	res := mapOne(t, lines(t)["fitness_data/weight"])
	require.Len(t, res.QuantitySamples, 2)
	require.Equal(t, "body_mass", res.QuantitySamples[0].QuantityType)
	require.Equal(t, 72.4, res.QuantitySamples[0].Value)
	require.Equal(t, "kg", res.QuantitySamples[0].Unit)
	require.Equal(t, at("2026-03-08T07:00:00Z"), res.QuantitySamples[0].StartDate)
	require.Equal(t, "body_fat_percentage", res.QuantitySamples[1].QuantityType)
	require.Equal(t, 18.2, res.QuantitySamples[1].Value)
	require.Equal(t, "percent", res.QuantitySamples[1].Unit)
}

func TestMapSleepWithHypnogram(t *testing.T) {
	res := mapOne(t, lines(t)["fitness_data/sleep"])
	require.Len(t, res.SleepSessions, 1)
	s := res.SleepSessions[0]
	require.Equal(t, at("2026-03-07T21:20:00Z"), s.BedtimeStart)
	require.Equal(t, at("2026-03-08T04:45:00Z"), s.BedtimeEnd)
	require.Equal(t, "2000000001_sleep_1772918400", s.ExternalID)
	require.Equal(t, 15300, *s.LightSleepSeconds)
	require.Equal(t, 5400, *s.DeepSleepSeconds)
	require.Equal(t, 5400, *s.REMSleepSeconds)
	require.Equal(t, 600, *s.AwakeSeconds)
	require.Equal(t, 26100, *s.TotalSleepSeconds)
	require.Equal(t, 82.0, *s.SleepScore, "the daily report's score")
	require.Equal(t, "Etc/GMT-1", s.Metadata["timezone"])
	require.Equal(t, 56.0, s.Metadata["avg_heart_rate"])

	require.Len(t, res.SleepPhases, 5)
	want := []string{model.SleepStageLight, model.SleepStageDeep, model.SleepStageREM, model.SleepStageAwake, model.SleepStageLight}
	for i, p := range res.SleepPhases {
		require.Equal(t, want[i], p.Phase)
		require.Equal(t, s.ContentHash, p.ParentContentHash)
	}
}

func TestMapNapWithoutHypnogram(t *testing.T) {
	res := mapOne(t, lines(t)["fitness_data/watch_daytime_sleep"])
	require.Len(t, res.SleepSessions, 1)
	s := res.SleepSessions[0]
	require.Empty(t, res.SleepPhases)
	require.Equal(t, 30*60, *s.LightSleepSeconds, "stage minutes of the report")
	require.Equal(t, 30*60, *s.TotalSleepSeconds)
	require.Nil(t, s.SleepScore)
}

func TestMapWorkouts(t *testing.T) {
	l := lines(t)
	res := mapOne(t, l["sport_record/outdoor_running"])
	require.Len(t, res.Workouts, 1)
	w := res.Workouts[0]
	require.Equal(t, "running", w.ActivityType)
	require.Equal(t, at("2026-03-08T10:00:00Z"), w.StartDate)
	require.Equal(t, at("2026-03-08T10:35:00Z"), w.EndDate)
	require.Equal(t, 2040, *w.DurationS)
	require.Equal(t, 5230.0, *w.DistanceM)
	require.Equal(t, 356.0, *w.ActiveEnergyKcal)
	require.Equal(t, 148.0, *w.AvgHeartRate)
	require.Equal(t, 171.0, *w.MaxHeartRate)
	require.Equal(t, 32.0, *w.ElevationGainM)
	require.Equal(t, 46.0, *w.VO2Max)
	require.Equal(t, "running", w.Metadata["mi_category"])
	require.Equal(t, "band.example.m1", w.Metadata["device_model"])
	require.True(t, w.Replaceable)

	w = mapOne(t, l["sport_record/dumbbell_training"]).Workouts[0]
	require.Equal(t, "traditional_strength_training", w.ActivityType)
	require.Nil(t, w.DistanceM)
}

func TestMapUnknownKeyIsSkipped(t *testing.T) {
	res := mapOne(t, lines(t)["fitness_data/stress"])
	require.Empty(t, res.QuantitySamples)
	require.Empty(t, res.SleepSessions)
}

func TestMapIsDeterministic(t *testing.T) {
	for k, l := range lines(t) {
		a, b := mapOne(t, l), mapOne(t, l)
		require.Equal(t, a, b, k)
	}
}

func TestMapErrors(t *testing.T) {
	for name, line := range map[string]string{
		"no time":            `{"table":"fitness_data","key":"heart_rate","value":{"bpm":60}}`,
		"workout no end":     `{"table":"sport_record","key":"outdoor_running","time":1772964000,"value":{"start_time":1772964000}}`,
		"workout ends early": `{"table":"sport_record","key":"outdoor_running","time":1772964000,"value":{"start_time":1772964000,"end_time":1772963000}}`,
	} {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		_, err := New().Map(connector.RawRecord{UserID: testUser, Provider: Slug, Fields: m})
		require.Error(t, err, name)
	}
	// Figures written as strings read as numbers; out of range ones drop.
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"table":"fitness_data","key":"spo2","time":1772935200,"value":{"spo2":"98"}}`), &m))
	require.Equal(t, 98.0, one(t, mapOne(t, m)).Value)
	require.NoError(t, json.Unmarshal([]byte(`{"table":"fitness_data","key":"spo2","time":1772935200,"value":{"spo2":140}}`), &m))
	require.Empty(t, mapOne(t, m).QuantitySamples)
}
