// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
	"github.com/soma-intelligence/open-health-sync/internal/model"
)

const testUser = "00000000-0000-0000-0000-000000000001"

// fields decodes the stored line of a file, as the ETL reader hands it to
// Map.
func fields(t testing.TB, format string, data []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(Line(format, data), &m))
	return m
}

func mapFile(t testing.TB, name string) model.MapResult {
	t.Helper()
	format := name[strings.LastIndexByte(name, '.')+1:]
	res, err := New().Map(connector.RawRecord{UserID: testUser, Provider: Slug, Fields: fields(t, format, fixture(t, name))})
	require.NoError(t, err)
	return res
}

func samples(res model.MapResult, qt string) []model.QuantitySample {
	var out []model.QuantitySample
	for _, s := range res.QuantitySamples {
		if s.QuantityType == qt {
			out = append(out, s)
		}
	}
	return out
}

func values(ss []model.QuantitySample) []float64 {
	out := make([]float64, len(ss))
	for i, s := range ss {
		out[i] = s.Value
	}
	return out
}

func f(v float64) *float64 { return &v }

func parseUpload(c *Connector, format string, body []byte) error {
	return c.ParseUpload(connector.UploadMeta{Type: format}, bytes.NewReader(body), nil)
}

func TestLine(t *testing.T) {
	l := Line(FormatGPX, []byte("<gpx/>"))
	require.Equal(t, `{"type":"gpx","data":"PGdweC8+"}`+"\n", string(l))
	require.True(t, ValidFormat("fit"))
	require.False(t, ValidFormat("kml"))
}

func TestParseUploadAccepts(t *testing.T) {
	c := New()
	for _, name := range []string{
		"garmin_ride.fit", "coros_run.fit", "wahoo_indoor.fit", "zwift_virtual.fit",
		"garmin_multisport.fit", "no_session.fit",
		"garmin_run.gpx", "strava_ride.gpx", "strava_ride.tcx", "polar_run.tcx",
	} {
		format := name[strings.LastIndexByte(name, '.')+1:]
		require.NoError(t, parseUpload(c, format, Line(format, fixture(t, name))), name)
	}
}

func TestParseUploadRefuses(t *testing.T) {
	c := New()
	ride := fixture(t, "garmin_ride.fit")
	b64 := base64.StdEncoding.EncodeToString(ride)
	cases := []struct {
		name   string
		format string
		body   string
		want   error
	}{
		{"empty", "fit", "\n \n", ErrNoLines},
		{"not json", "fit", "nope\n", ErrInvalidLine},
		{"array", "fit", "[1]\n", ErrInvalidLine},
		{"unknown type", "kml", `{"type":"kml","data":"AA=="}`, ErrInvalidLine},
		{"no data", "fit", `{"type":"fit"}`, ErrInvalidLine},
		{"bad base64", "fit", `{"type":"fit","data":"***"}`, ErrInvalidLine},
		{"type differs from header", "gpx", `{"type":"fit","data":"` + b64 + `"}`, ErrInvalidLine},
		{"two lines", "fit", string(Line("fit", ride)) + string(Line("fit", ride)), ErrInvalidLine},
		{"not a FIT file", "fit", string(Line("fit", []byte("this is not a FIT file at all"))), ErrInvalidFile},
		{"broken CRC", "fit", string(Line("fit", fixture(t, "bad_crc.fit"))), ErrInvalidFile},
		{"truncated FIT", "fit", string(Line("fit", ride[:len(ride)/2])), ErrInvalidFile},
		{"monitoring FIT", "fit", string(Line("fit", fixture(t, "monitoring.fit"))), ErrNotActivity},
		{"GPX route", "gpx", string(Line("gpx", fixture(t, "route.gpx"))), ErrNotActivity},
		{"broken XML", "gpx", string(Line("gpx", []byte("<gpx><trk>"))), ErrInvalidFile},
		{"not XML", "tcx", string(Line("tcx", []byte("hello"))), ErrInvalidFile},
		{"TCX without activity", "tcx", string(Line("tcx", []byte("<TrainingCenterDatabase><Courses/></TrainingCenterDatabase>"))), ErrNotActivity},
		{"too large", "gpx", string(Line("gpx", make([]byte, MaxFileBytes+1))), ErrTooLarge},
	}
	for _, tc := range cases {
		err := parseUpload(c, tc.format, []byte(tc.body))
		require.ErrorIs(t, err, tc.want, tc.name)
		// The message names the problem, never the file's content.
		require.NotContains(t, err.Error(), b64[:16], tc.name)
		require.NotContains(t, err.Error(), "this is not", tc.name)
	}
}

func TestParseUploadRefusesFutureStart(t *testing.T) {
	c := New()
	c.now = func() time.Time { return fixtureStart.Add(-72 * time.Hour) }
	err := parseUpload(c, "fit", Line("fit", fixture(t, "garmin_ride.fit")))
	require.ErrorIs(t, err, ErrNotActivity)
	c.now = func() time.Time { return fixtureStart.Add(-24 * time.Hour) }
	require.NoError(t, parseUpload(c, "fit", Line("fit", fixture(t, "garmin_ride.fit"))))
}

func TestParseUploadRecoversFromPanics(t *testing.T) {
	// Arbitrary bytes after a valid FIT header must give an error, not a
	// panic, whatever the decoder does with them.
	ride := fixture(t, "garmin_ride.fit")
	for i := 14; i < len(ride); i += 7 {
		b := bytes.Clone(ride)
		b[i] ^= 0x5A
		_, err := Inspect("fit", b)
		if err != nil {
			require.ErrorIs(t, err, ErrInvalidFile)
		}
	}
}

func TestInspect(t *testing.T) {
	in, err := Inspect("fit", fixture(t, "garmin_multisport.fit"))
	require.NoError(t, err)
	require.Equal(t, Info{Format: "fit", Start: fixtureStart, Workouts: 2, RoutePoints: 8, HeartRate: 8, Power: 4}, in)

	in, err = Inspect("tcx", fixture(t, "strava_ride.tcx"))
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 4, 6, 30, 0, 0, time.UTC), in.Start)
}

func TestMapGarminRide(t *testing.T) {
	res := mapFile(t, "garmin_ride.fit")
	require.Len(t, res.Workouts, 1)
	w := res.Workouts[0]
	require.Equal(t, Slug, w.Provider)
	require.Equal(t, "garmin", w.SourceSlug)
	require.Equal(t, "Garmin edge_530", w.DeviceID)
	require.Equal(t, "cycling", w.ActivityType)
	require.Equal(t, fixtureStart, w.StartDate)
	require.Equal(t, fixtureStart.Add(10*time.Second), w.EndDate)
	require.Equal(t, 9, *w.DurationS)
	require.Equal(t, f(85.5), w.DistanceM)
	require.Equal(t, f(12), w.ActiveEnergyKcal)
	require.Equal(t, f(124), w.AvgHeartRate)
	require.Equal(t, f(129), w.MaxHeartRate)
	require.Equal(t, f(9), w.ElevationGainM)
	require.Equal(t, f(8.45), w.AvgSpeedMps)
	require.Nil(t, w.VO2Max)
	require.False(t, w.Replaceable)
	require.Regexp(t, `^af-[0-9a-f]{20}-0$`, w.ExternalID)
	md := w.Metadata
	require.Equal(t, "fit", md["file_format"])
	require.Len(t, md["file_sha256"], 64)
	require.Equal(t, "cycling", md["fit_sport"])
	require.Equal(t, "road", md["fit_sub_sport"])
	require.Equal(t, "edge_530", md["device_name"])
	require.Equal(t, 60, md["utc_offset_minutes"])
	require.Equal(t, "Etc/GMT-1", md["timezone"])
	require.Equal(t, 10.0, md["elapsed_time_s"])
	require.Equal(t, 9.0, md["moving_time_s"])
	require.Equal(t, 2.0, md["total_descent_m"])
	require.Equal(t, 8.9, md["max_speed_mps"])
	require.Equal(t, 204.0, md["avg_power_w"])
	require.Equal(t, 209.0, md["max_power_w"])
	require.Equal(t, 205.0, md["normalized_power_w"])
	require.Equal(t, 88.0, md["avg_cadence"])
	require.Equal(t, 95.0, md["max_cadence"])
	require.NotContains(t, md, "indoor")

	// Ten records: one without a fix, so nine route points.
	require.Len(t, res.RoutePoints, 9)
	for _, rp := range res.RoutePoints {
		require.Equal(t, w.ContentHash, rp.ParentContentHash)
		require.NotNil(t, rp.AltitudeM)
		require.NotNil(t, rp.SpeedMps)
	}
	require.InDelta(t, 45.1, res.RoutePoints[0].Latitude, 1e-6)
	require.InDelta(t, 6.1, res.RoutePoints[0].Longitude, 1e-6)
	require.InDelta(t, 400, *res.RoutePoints[0].AltitudeM, 0.2)
	require.InDelta(t, 8, *res.RoutePoints[0].SpeedMps, 0.001)

	// Heart rate: record 3 has none, record 2 is below the hard band, and
	// the second record at second 5 is left out.
	hr := samples(res, model.QTHeartRate)
	require.Equal(t, []float64{120, 121, 124, 125, 126, 127, 128, 129}, values(hr))
	s := hr[0]
	require.Equal(t, "bpm", s.Unit)
	require.Equal(t, "garmin", s.SourceSlug)
	require.Equal(t, "Garmin", s.SourceName)
	require.Equal(t, "Garmin edge_530", s.DeviceID)
	require.Equal(t, "edge_530", s.DeviceModel)
	require.Equal(t, fixtureStart, s.StartDate)
	require.Equal(t, s.StartDate, s.EndDate)
	require.Equal(t, map[string]any{"timezone": "Etc/GMT-1"}, s.Metadata)
	require.False(t, s.Replaceable)
	require.True(t, s.IsPrimarySource)
	// One metadata map per sample: the validator flags samples one by one.
	hr[0].Metadata["probe"] = true
	require.NotContains(t, hr[1].Metadata, "probe")

	pw := samples(res, "cycling_power")
	require.Equal(t, []float64{200, 201, 202, 203, 204, 205, 207, 208, 209}, values(pw))
	require.Equal(t, "W", pw[0].Unit)
	require.Empty(t, samples(res, "running_power"))
	require.True(t, res.HighFrequency)
}

func TestMapIsDeterministic(t *testing.T) {
	a, b := mapFile(t, "garmin_ride.fit"), mapFile(t, "garmin_ride.fit")
	require.Equal(t, a, b)
	seen := map[string]bool{}
	for _, s := range a.QuantitySamples {
		require.False(t, seen[s.ContentHash], "two samples share a hash")
		seen[s.ContentHash] = true
	}
}

func TestMapCorosRun(t *testing.T) {
	res := mapFile(t, "coros_run.fit")
	w := res.Workouts[0]
	require.Equal(t, "coros", w.SourceSlug)
	require.Equal(t, "COROS PACE 3", w.DeviceID)
	require.Equal(t, "running", w.ActivityType)
	require.Equal(t, "trail", w.Metadata["fit_sub_sport"])
	// UTC+5:30 is kept as an offset; no Etc/GMT zone exists for it.
	require.Equal(t, 330, w.Metadata["utc_offset_minutes"])
	require.NotContains(t, w.Metadata, "timezone")
	require.Len(t, res.RoutePoints, 8)
	require.Len(t, samples(res, "running_power"), 8)
	require.Empty(t, samples(res, "cycling_power"))
	require.Nil(t, samples(res, model.QTHeartRate)[0].Metadata)
}

func TestMapWahooIndoor(t *testing.T) {
	res := mapFile(t, "wahoo_indoor.fit")
	w := res.Workouts[0]
	require.Equal(t, "wahoo", w.SourceSlug)
	require.Equal(t, "Wahoo ELEMNT BOLT", w.DeviceID)
	require.Equal(t, "cycling", w.ActivityType)
	require.Equal(t, true, w.Metadata["indoor"])
	require.Equal(t, "Etc/GMT+5", w.Metadata["timezone"])
	require.Nil(t, w.DistanceM)
	require.Nil(t, w.AvgSpeedMps)
	require.Empty(t, res.RoutePoints)
	require.Len(t, samples(res, model.QTHeartRate), 6)
	require.Len(t, samples(res, "cycling_power"), 6)
}

func TestMapZwiftVirtualKeepsNoRoute(t *testing.T) {
	res := mapFile(t, "zwift_virtual.fit")
	w := res.Workouts[0]
	require.Equal(t, "zwift", w.SourceSlug)
	require.Empty(t, w.DeviceID)
	require.Equal(t, true, w.Metadata["virtual"])
	require.Equal(t, true, w.Metadata["indoor"])
	require.Equal(t, "Etc/UTC", w.Metadata["timezone"])
	require.Empty(t, res.RoutePoints, "the coordinates of a virtual ride are a game map")
	require.Len(t, samples(res, model.QTHeartRate), 5)
	require.Len(t, samples(res, "cycling_power"), 5)
}

func TestMapMultisport(t *testing.T) {
	res := mapFile(t, "garmin_multisport.fit")
	require.Len(t, res.Workouts, 2)
	swim, bike := res.Workouts[0], res.Workouts[1]
	require.Equal(t, "swimming", swim.ActivityType)
	require.Equal(t, "cycling", bike.ActivityType)
	require.Equal(t, "Garmin fr965", swim.DeviceID)
	require.Equal(t, fixtureStart, swim.StartDate)
	require.Equal(t, fixtureStart.Add(10*time.Second), bike.StartDate)
	require.NotEqual(t, swim.ContentHash, bike.ContentHash)
	require.Equal(t, 0, swim.Metadata["session_index"])
	require.Equal(t, 1, bike.Metadata["session_index"])
	require.Equal(t, 2, bike.Metadata["sessions"])
	require.Regexp(t, `-1$`, bike.ExternalID)

	byParent := map[string]int{}
	for _, rp := range res.RoutePoints {
		byParent[rp.ParentContentHash]++
	}
	require.Equal(t, map[string]int{swim.ContentHash: 4, bike.ContentHash: 4}, byParent)
	require.Len(t, samples(res, model.QTHeartRate), 8)
	// The swim's power figure has no quantity; the ride's four do.
	require.Equal(t, []float64{220, 221, 222, 223}, values(samples(res, "cycling_power")))
}

func TestMapFITWithoutSession(t *testing.T) {
	res := mapFile(t, "no_session.fit")
	require.Len(t, res.Workouts, 1)
	w := res.Workouts[0]
	require.Equal(t, Slug, w.SourceSlug, "a development manufacturer names no vendor")
	require.Equal(t, "running", w.ActivityType)
	require.Equal(t, true, w.Metadata["indoor"])
	require.Equal(t, fixtureStart, w.StartDate)
	require.Equal(t, fixtureStart.Add(2*time.Second), w.EndDate)
	require.Equal(t, 2, *w.DurationS)
	require.Equal(t, f(151), w.AvgHeartRate)
	require.Len(t, samples(res, model.QTHeartRate), 3)
}

func TestMapGarminGPX(t *testing.T) {
	res := mapFile(t, "garmin_run.gpx")
	require.Len(t, res.Workouts, 1)
	w := res.Workouts[0]
	start := time.Date(2026, 3, 2, 7, 0, 0, 0, time.UTC)
	require.Equal(t, "garmin", w.SourceSlug)
	require.Empty(t, w.DeviceID)
	require.Equal(t, "running", w.ActivityType)
	require.Equal(t, "running", w.Metadata["gpx_type"])
	require.Equal(t, start, w.StartDate)
	require.Equal(t, start.Add(4*time.Second), w.EndDate)
	require.Equal(t, 4, *w.DurationS)
	require.InDelta(t, 54.6, *w.DistanceM, 0.5) // four steps of about 13.7 m
	require.InDelta(t, *w.DistanceM/4, *w.AvgSpeedMps, 1e-9)
	require.Equal(t, f(142), w.AvgHeartRate)
	require.Equal(t, f(144), w.MaxHeartRate)
	require.Nil(t, w.ElevationGainM, "no gain is summed from GPS altitude")
	require.Nil(t, w.ActiveEnergyKcal)
	require.NotContains(t, w.Metadata, "timezone")
	require.Len(t, res.RoutePoints, 5)
	require.Equal(t, []float64{140, 141, 142, 143, 144}, values(samples(res, model.QTHeartRate)))
	require.Nil(t, samples(res, model.QTHeartRate)[0].Metadata)
	require.Empty(t, samples(res, "running_power"))
}

func TestMapStravaGPX(t *testing.T) {
	res := mapFile(t, "strava_ride.gpx")
	w := res.Workouts[0]
	require.Equal(t, "strava", w.SourceSlug)
	require.Equal(t, "cycling", w.ActivityType)
	require.Equal(t, f(126), w.AvgHeartRate)
	require.Equal(t, 6000.0, w.Metadata["max_power_w"], "the file's own maximum is kept as written")
	// The 6000 W reading is above the hard band of cycling power.
	require.Equal(t, []float64{210, 215}, values(samples(res, "cycling_power")))
	require.InDelta(t, 7.5, *res.RoutePoints[0].SpeedMps, 1e-9)
	require.Nil(t, res.RoutePoints[2].SpeedMps)
}

func TestMapTCX(t *testing.T) {
	res := mapFile(t, "polar_run.tcx")
	require.Len(t, res.Workouts, 1)
	w := res.Workouts[0]
	start := time.Date(2026, 3, 5, 18, 0, 0, 0, time.UTC)
	require.Equal(t, "polar", w.SourceSlug)
	require.Equal(t, "Polar Vantage V3", w.DeviceID)
	require.Equal(t, "running", w.ActivityType)
	require.Equal(t, "Running", w.Metadata["tcx_sport"])
	require.Equal(t, start, w.StartDate)
	require.Equal(t, start.Add(8*time.Second), w.EndDate)
	require.Equal(t, 8, *w.DurationS)
	require.InDelta(t, 24.4, *w.DistanceM, 1e-9)
	require.Equal(t, f(4), w.ActiveEnergyKcal)
	require.Equal(t, f(152), w.AvgHeartRate) // time weighted over the laps
	require.Equal(t, f(156), w.MaxHeartRate)
	require.InDelta(t, 3.05, *w.AvgSpeedMps, 1e-9)
	require.Equal(t, 3.3, w.Metadata["max_speed_mps"])
	require.Len(t, res.RoutePoints, 3, "the last track point has no position")
	require.Equal(t, []float64{148, 152, 154, 156}, values(samples(res, model.QTHeartRate)))
	require.Equal(t, []float64{260, 270, 265}, values(samples(res, "running_power")))
	require.Equal(t, "Polar", samples(res, model.QTHeartRate)[0].SourceName)
}

func TestMapStravaTCXWithLeadingSpaces(t *testing.T) {
	res := mapFile(t, "strava_ride.tcx")
	w := res.Workouts[0]
	require.Equal(t, Slug, w.SourceSlug, "the file names no vendor")
	require.Equal(t, "cycling", w.ActivityType)
	require.Equal(t, f(48), w.DistanceM)
	require.Equal(t, f(120), w.AvgHeartRate, "no lap average: the mean of the track points")
	require.Len(t, res.RoutePoints, 3)
	require.Len(t, samples(res, "cycling_power"), 3)
}

func TestSameReadingInTwoFormatsIsOneSample(t *testing.T) {
	// A FIT and a GPX of the same Garmin run: the same reading hashes the
	// same, the workouts and their routes do not.
	fitRes := mapFile(t, "garmin_ride.fit")
	gpx := []byte(`<gpx creator="Garmin Connect"><trk><type>cycling</type><trkseg>
<trkpt lat="45.1" lon="6.1"><time>2026-03-01T08:00:00Z</time><extensions><hr>120</hr></extensions></trkpt>
</trkseg></trk></gpx>`)
	res, err := New().Map(connector.RawRecord{UserID: testUser, Fields: fields(t, "gpx", gpx)})
	require.NoError(t, err)
	require.Equal(t, samples(fitRes, model.QTHeartRate)[0].ContentHash, samples(res, model.QTHeartRate)[0].ContentHash)
	require.NotEqual(t, fitRes.Workouts[0].ContentHash, res.Workouts[0].ContentHash)
}

func TestWorkoutHashIgnoresFileBytes(t *testing.T) {
	// The same activity saved twice (other bytes, same device and start) is
	// the same workout: the first stored wins.
	a := mapFile(t, "polar_run.tcx")
	other := bytes.Replace(fixture(t, "polar_run.tcx"), []byte("<Calories>2</Calories>"), []byte("<Calories>3</Calories>"), 1)
	b, err := New().Map(connector.RawRecord{UserID: testUser, Fields: fields(t, "tcx", other)})
	require.NoError(t, err)
	require.Equal(t, a.Workouts[0].ContentHash, b.Workouts[0].ContentHash)
	require.NotEqual(t, a.Workouts[0].Metadata["file_sha256"], b.Workouts[0].Metadata["file_sha256"])
}

func TestMapRefusesBadLine(t *testing.T) {
	_, err := New().Map(connector.RawRecord{UserID: testUser, Fields: map[string]any{"type": "fit"}})
	require.ErrorIs(t, err, ErrInvalidLine)
	_, err = New().Map(connector.RawRecord{UserID: testUser, Fields: fields(t, "fit", fixture(t, "monitoring.fit"))})
	require.ErrorIs(t, err, ErrNotActivity)
}

func TestFITActivity(t *testing.T) {
	for _, tc := range []struct {
		sport, sub, want string
		indoor, virtual  bool
	}{
		{"running", "treadmill", "running", true, false},
		{"fitness_equipment", "treadmill", "running", true, false},
		{"fitness_equipment", "elliptical", "elliptical", false, false},
		{"fitness_equipment", "indoor_rowing", "rowing", true, false},
		{"cycling", "hand_cycling", "hand_cycling", false, false},
		{"cycling", "virtual_activity", "cycling", true, true},
		{"training", "strength_training", "traditional_strength_training", false, false},
		{"training", "yoga", "yoga", false, false},
		{"racket", "pickleball", "pickleball", false, false},
		{"alpine_skiing", "resort", "downhill_skiing", false, false},
		{"generic", "", "other", false, false},
		{"", "", "other", false, false},
	} {
		a, in, v := fitActivity(tc.sport, tc.sub)
		require.Equal(t, tc.want, a, tc.sport+"/"+tc.sub)
		require.Equal(t, tc.indoor, in, tc.sport+"/"+tc.sub)
		require.Equal(t, tc.virtual, v, tc.sport+"/"+tc.sub)
	}
}

func TestSources(t *testing.T) {
	require.Equal(t, source{slug: "garmin", name: "Garmin"}, fitSource("garmin"))
	require.Equal(t, source{slug: "wahoo", name: "Wahoo"}, fitSource("wahoo_fitness"))
	require.Equal(t, source{slug: "polar", name: "Polar"}, fitSource("polar_electro"))
	require.Equal(t, source{slug: "favero_electronics"}, fitSource("favero_electronics"))
	require.Equal(t, source{slug: Slug}, fitSource("development"))
	require.Equal(t, source{slug: Slug}, fitSource("ManufacturerInvalid(9999)"))
	require.Equal(t, source{slug: Slug}, fitSource(""))

	require.Equal(t, "garmin", textSource("Forerunner 965", "").slug)
	require.Equal(t, "garmin", textSource("fēnix 8").slug)
	require.Equal(t, "wahoo", textSource("ELEMNT ROAM").slug)
	require.Equal(t, "hammerhead", textSource("Karoo 3").slug)
	require.Equal(t, "strava", textSource("", "StravaGPX iPhone").slug)
	require.Equal(t, Slug, textSource("Invented planner").slug)
	require.Equal(t, "Polar Vantage V3", textSource("Polar Vantage V3").withDevice("Polar Vantage V3").deviceID)
	require.Equal(t, "Garmin Forerunner 965", textSource("Forerunner 965").withDevice("Forerunner 965").deviceID)
}

func TestOffsetZone(t *testing.T) {
	require.Equal(t, "Etc/UTC", offsetZone(0))
	require.Equal(t, "Etc/GMT-2", offsetZone(120))
	require.Equal(t, "Etc/GMT+5", offsetZone(-300))
	require.Equal(t, "", offsetZone(330))
	_, ok := fitOffset(fixtureStart, fixtureStart.Add(15*time.Hour))
	require.False(t, ok)
	_, ok = fitOffset(fixtureStart, fixtureStart.Add(7*time.Minute))
	require.False(t, ok)
}

// FuzzInspect checks that no input makes a decoder panic or hang. `go test`
// runs the seeds; `go test -fuzz FuzzInspect` explores further.
func FuzzInspect(f *testing.F) {
	for _, name := range []string{"garmin_ride.fit", "garmin_multisport.fit", "garmin_run.gpx", "polar_run.tcx"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		require.NoError(f, err)
		f.Add(name[strings.LastIndexByte(name, '.')+1:], b)
	}
	f.Fuzz(func(t *testing.T, format string, data []byte) {
		in, err := Inspect(format, data)
		if err == nil {
			require.False(t, in.Start.IsZero())
			require.Positive(t, in.Workouts)
		}
	})
}

func TestXMLLatin1(t *testing.T) {
	gpx := []byte("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<gpx creator=\"Ex\xe9mple\"><trk><trkseg>" +
		"<trkpt lat=\"1\" lon=\"1\"><time>2026-03-01T08:00:00</time></trkpt></trkseg></trk></gpx>")
	in, err := Inspect("gpx", gpx)
	require.NoError(t, err)
	require.Equal(t, fixtureStart, in.Start, "a time without an offset is UTC")
}
