// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the FIT fixtures in testdata")

// Every value in the fixtures is invented: times in March 2026, positions on
// a made-up loop, heart rate and power counting up from round numbers.
var fixtureStart = time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

// fitRecord is one record of a fixture; zero fields are left out.
type fitRecord struct {
	at       int // seconds after the session start
	lat, lon float64
	alt      float64
	speed    float64
	hr       uint8
	power    uint16
	cadence  uint8
}

// fitSessionSpec is one session of a fixture.
type fitSessionSpec struct {
	sport    typedef.Sport
	subSport typedef.SubSport
	offset   int // seconds after fixtureStart
	elapsed  float64
	timer    float64
	distance float64
	kcal     uint16
	avgHR    uint8
	maxHR    uint8
	ascent   uint16
	descent  uint16
	avgSpeed float64
	maxSpeed float64
	avgPower uint16
	maxPower uint16
	normPow  uint16
	avgCad   uint8
	maxCad   uint8
	records  []fitRecord
}

// fitSpec describes a whole fixture file.
type fitSpec struct {
	fileType     typedef.File
	manufacturer typedef.Manufacturer
	product      uint16
	productName  string
	creatorName  string // device_info of the creator
	localOffset  time.Duration
	noActivity   bool
	sport        *mesgdef.Sport // a sport message, for a file without sessions
	sessions     []fitSessionSpec
	noSessions   bool
}

// buildFIT encodes a fixture with the library's encoder.
func buildFIT(t testing.TB, s fitSpec) []byte {
	t.Helper()
	act := filedef.NewActivity()
	act.FileId = *mesgdef.NewFileId(nil).
		SetType(s.fileType).
		SetManufacturer(s.manufacturer).
		SetProduct(s.product).
		SetSerialNumber(1234567890).
		SetTimeCreated(fixtureStart)
	if s.productName != "" {
		act.FileId.SetProductName(s.productName)
	}
	if s.creatorName != "" {
		act.DeviceInfos = append(act.DeviceInfos, mesgdef.NewDeviceInfo(nil).
			SetTimestamp(fixtureStart).
			SetDeviceIndex(typedef.DeviceIndexCreator).
			SetManufacturer(s.manufacturer).
			SetProductName(s.creatorName))
	}
	if s.sport != nil {
		act.Sports = append(act.Sports, s.sport)
	}
	var last time.Time
	for _, ss := range s.sessions {
		start := fixtureStart.Add(time.Duration(ss.offset) * time.Second)
		for _, r := range ss.records {
			at := start.Add(time.Duration(r.at) * time.Second)
			rec := mesgdef.NewRecord(nil).SetTimestamp(at)
			if r.lat != 0 || r.lon != 0 {
				rec.SetPositionLatDegrees(r.lat).SetPositionLongDegrees(r.lon)
			}
			if r.alt != 0 {
				rec.SetEnhancedAltitudeScaled(r.alt)
			}
			if r.speed != 0 {
				rec.SetEnhancedSpeedScaled(r.speed)
			}
			if r.hr != 0 {
				rec.SetHeartRate(r.hr)
			}
			if r.power != 0 {
				rec.SetPower(r.power)
			}
			if r.cadence != 0 {
				rec.SetCadence(r.cadence)
			}
			act.Records = append(act.Records, rec)
			if at.After(last) {
				last = at
			}
		}
		if s.noSessions {
			continue
		}
		end := start.Add(time.Duration(ss.elapsed * float64(time.Second)))
		sess := mesgdef.NewSession(nil).
			SetTimestamp(end).
			SetStartTime(start).
			SetEvent(typedef.EventTimer).
			SetEventType(typedef.EventTypeStop).
			SetSport(ss.sport).
			SetSubSport(ss.subSport).
			SetTotalElapsedTimeScaled(ss.elapsed).
			SetTotalTimerTimeScaled(ss.timer)
		if ss.distance > 0 {
			sess.SetTotalDistanceScaled(ss.distance)
		}
		if ss.kcal > 0 {
			sess.SetTotalCalories(ss.kcal)
		}
		if ss.avgHR > 0 {
			sess.SetAvgHeartRate(ss.avgHR).SetMaxHeartRate(ss.maxHR)
		}
		if ss.ascent > 0 || ss.descent > 0 {
			sess.SetTotalAscent(ss.ascent).SetTotalDescent(ss.descent)
		}
		if ss.avgSpeed > 0 {
			sess.SetEnhancedAvgSpeedScaled(ss.avgSpeed).SetEnhancedMaxSpeedScaled(ss.maxSpeed)
		}
		if ss.avgPower > 0 {
			sess.SetAvgPower(ss.avgPower).SetMaxPower(ss.maxPower)
		}
		if ss.normPow > 0 {
			sess.SetNormalizedPower(ss.normPow)
		}
		if ss.avgCad > 0 {
			sess.SetAvgCadence(ss.avgCad).SetMaxCadence(ss.maxCad)
		}
		act.Sessions = append(act.Sessions, sess)
		if end.After(last) {
			last = end
		}
	}
	if !s.noActivity {
		act.Activity = mesgdef.NewActivity(nil).
			SetTimestamp(last).
			SetLocalTimestamp(last.Add(s.localOffset)).
			SetNumSessions(uint16(len(act.Sessions))).
			SetType(typedef.ActivityManual).
			SetEvent(typedef.EventActivity).
			SetEventType(typedef.EventTypeStop)
	}
	fit := act.ToFIT(nil)
	var buf bytes.Buffer
	require.NoError(t, encoder.New(&buf).Encode(&fit))
	return buf.Bytes()
}

// loop returns n records one second apart on an invented loop, with heart
// rate from hr and power from power (0 leaves them out).
func loop(n int, lat, lon float64, hr uint8, power uint16) []fitRecord {
	out := make([]fitRecord, n)
	for i := range out {
		out[i] = fitRecord{
			at:    i,
			lat:   lat + float64(i)*0.0001,
			lon:   lon + float64(i)*0.0001,
			alt:   400 + float64(i),
			speed: 8 + float64(i)*0.1,
		}
		if hr > 0 {
			out[i].hr = hr + uint8(i)
		}
		if power > 0 {
			out[i].power = power + uint16(i)
		}
	}
	return out
}

// fitFixtures are the FIT files under testdata, by name.
func fitFixtures(t testing.TB) map[string][]byte {
	ride := loop(10, 45.1, 6.1, 120, 200)
	ride[3].hr = 0                  // no strap reading
	ride[4].lat, ride[4].lon = 0, 0 // no GPS fix
	ride[6].power = 0               // no power reading
	ride[2].hr = 15                 // below the hard band: left out
	// A second record at the same second: its heart rate is left out.
	ride = append(ride, fitRecord{at: 5, hr: 200})

	run := loop(8, 45.2, 6.2, 140, 250)
	for i := range run {
		run[i].speed = 3 + float64(i)*0.05
	}

	indoor := loop(6, 0, 0, 110, 150)
	for i := range indoor {
		indoor[i].lat, indoor[i].lon, indoor[i].alt, indoor[i].speed = 0, 0, 0, 0
	}

	virtual := loop(5, -11.6, 166.9, 130, 180)

	treadmill := loop(3, 0, 0, 150, 0)
	for i := range treadmill {
		treadmill[i].lat, treadmill[i].lon, treadmill[i].alt = 0, 0, 0
	}

	swim := loop(4, 45.3, 6.3, 100, 0)
	swim[1].power = 90 // a swim has no power quantity: left out
	bike := loop(4, 45.31, 6.31, 135, 220)

	m := map[string][]byte{
		"garmin_ride.fit": buildFIT(t, fitSpec{
			fileType: typedef.FileActivity, manufacturer: typedef.ManufacturerGarmin,
			product: uint16(typedef.GarminProductEdge530), localOffset: time.Hour,
			sessions: []fitSessionSpec{{
				sport: typedef.SportCycling, subSport: typedef.SubSportRoad,
				elapsed: 10, timer: 9, distance: 85.5, kcal: 12, avgHR: 124, maxHR: 129,
				ascent: 9, descent: 2, avgSpeed: 8.45, maxSpeed: 8.9,
				avgPower: 204, maxPower: 209, normPow: 205, avgCad: 88, maxCad: 95,
				records: ride,
			}},
		}),
		"coros_run.fit": buildFIT(t, fitSpec{
			fileType: typedef.FileActivity, manufacturer: typedef.ManufacturerCoros,
			product: 841, productName: "PACE 3", localOffset: 5*time.Hour + 30*time.Minute,
			sessions: []fitSessionSpec{{
				sport: typedef.SportRunning, subSport: typedef.SubSportTrail,
				elapsed: 8, timer: 8, distance: 25, kcal: 3, avgHR: 143, maxHR: 147,
				avgSpeed: 3.17, maxSpeed: 3.35, avgPower: 253, maxPower: 257, avgCad: 84, maxCad: 86,
				records: run,
			}},
		}),
		"wahoo_indoor.fit": buildFIT(t, fitSpec{
			fileType: typedef.FileActivity, manufacturer: typedef.ManufacturerWahooFitness,
			product: 31, creatorName: "ELEMNT BOLT", localOffset: -5 * time.Hour,
			sessions: []fitSessionSpec{{
				sport: typedef.SportCycling, subSport: typedef.SubSportIndoorCycling,
				elapsed: 6, timer: 6, kcal: 4, avgHR: 112, maxHR: 115, avgPower: 152, maxPower: 155,
				records: indoor,
			}},
		}),
		"zwift_virtual.fit": buildFIT(t, fitSpec{
			fileType: typedef.FileActivity, manufacturer: typedef.ManufacturerZwift,
			product: 0, localOffset: 0,
			sessions: []fitSessionSpec{{
				sport: typedef.SportCycling, subSport: typedef.SubSportVirtualActivity,
				elapsed: 5, timer: 5, distance: 41, kcal: 2, avgHR: 132, maxHR: 134,
				avgPower: 182, maxPower: 184,
				records: virtual,
			}},
		}),
		"garmin_multisport.fit": buildFIT(t, fitSpec{
			fileType: typedef.FileActivity, manufacturer: typedef.ManufacturerGarmin,
			product: uint16(typedef.GarminProductFr965), localOffset: 2 * time.Hour,
			sessions: []fitSessionSpec{
				{
					sport: typedef.SportSwimming, subSport: typedef.SubSportOpenWater,
					elapsed: 4, timer: 4, distance: 6, kcal: 1, avgHR: 101, maxHR: 103,
					records: swim,
				},
				{
					sport: typedef.SportCycling, subSport: typedef.SubSportRoad, offset: 10,
					elapsed: 4, timer: 4, distance: 33, kcal: 2, avgHR: 136, maxHR: 138,
					avgPower: 221, maxPower: 223,
					records: bike,
				},
			},
		}),
		"no_session.fit": buildFIT(t, fitSpec{
			fileType: typedef.FileActivity, manufacturer: typedef.ManufacturerDevelopment,
			sport:      mesgdef.NewSport(nil).SetSport(typedef.SportRunning).SetSubSport(typedef.SubSportTreadmill),
			noSessions: true,
			sessions:   []fitSessionSpec{{records: treadmill}},
		}),
		"monitoring.fit": buildFIT(t, fitSpec{
			fileType: typedef.FileMonitoringB, manufacturer: typedef.ManufacturerGarmin,
			product: uint16(typedef.GarminProductFr965), noActivity: true,
		}),
	}
	broken := bytes.Clone(m["garmin_ride.fit"])
	broken[len(broken)-1] ^= 0xFF
	m["bad_crc.fit"] = broken
	return m
}

// TestFITFixtures keeps the committed FIT fixtures equal to what the
// builder writes. Run with -update after changing a fixture.
func TestFITFixtures(t *testing.T) {
	for name, want := range fitFixtures(t) {
		path := filepath.Join("testdata", name)
		if *update {
			require.NoError(t, os.WriteFile(path, want, 0o644))
			continue
		}
		got, err := os.ReadFile(path)
		require.NoError(t, err, "run go test ./connectors/activityfiles -run TestFITFixtures -update")
		require.True(t, bytes.Equal(want, got), "%s differs from the builder: run with -update", name)
	}
}

// fixture reads a file of testdata.
func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}
