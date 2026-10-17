// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// minStart is the earliest start accepted. FIT devices write times below
// 1998 when their clock is not set (seconds since power on), which are not
// dates.
var minStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// activity is a parsed file, whatever its format.
type activity struct {
	format string
	sha    string // hex SHA-256 of the file

	src source

	// offset is the UTC offset of the activity in minutes, when the file
	// states it (FIT only).
	offset    int
	hasOffset bool

	sessions []session
	points   []point // sorted by time
}

// session is one workout of the file: a FIT session, a TCX activity, the
// track of a GPX file. Figures are NaN when the file does not state them.
type session struct {
	sport    string // the file's own sport name (FIT sport, TCX Sport, GPX type)
	subSport string // FIT sub sport
	activity string // canonical activity type
	indoor   bool
	virtual  bool   // a virtual ride or run: the coordinates are a game map
	power    string // quantity type of its power readings, "" for none

	start, end time.Time
	timerS     float64 // moving time, pauses excluded
	elapsedS   float64

	distanceM, kcal        float64
	avgHR, maxHR           float64
	ascentM, descentM      float64
	avgSpeed, maxSpeed     float64
	avgPower, maxPower     float64
	normPower              float64
	avgCadence, maxCadence float64
}

// newSession returns a session with every figure missing.
func newSession() session {
	n := math.NaN()
	return session{
		timerS: n, elapsedS: n, distanceM: n, kcal: n, avgHR: n, maxHR: n,
		ascentM: n, descentM: n, avgSpeed: n, maxSpeed: n,
		avgPower: n, maxPower: n, normPower: n, avgCadence: n, maxCadence: n,
	}
}

// point is one timed reading: a GPS fix, a heart rate, a power figure, or
// several. Missing figures are NaN. sess is the index of the session it
// belongs to, -1 for none.
type point struct {
	t        time.Time
	lat, lon float64
	alt      float64
	speed    float64
	hr       float64
	power    float64
	sess     int
}

// nan marks a missing figure.
var nan = math.NaN()

// sortPoints sorts points by time, keeping the file's order for equal times.
func sortPoints(pts []point) {
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].t.Before(pts[j].t) })
}

// newPoint returns a point at t with every figure missing.
func newPoint(t time.Time) point {
	n := math.NaN()
	return point{t: t, lat: n, lon: n, alt: n, speed: n, hr: n, power: n, sess: -1}
}

func (p point) hasPos() bool {
	return !math.IsNaN(p.lat) && !math.IsNaN(p.lon) &&
		p.lat >= -90 && p.lat <= 90 && p.lon >= -180 && p.lon <= 180 &&
		(p.lat != 0 || p.lon != 0)
}

// parse decodes a file of the given format. A panic in a decoder becomes
// ErrInvalidFile: the file is input from anyone with an upload token.
func parse(format string, data []byte) (a *activity, err error) {
	if len(data) > MaxFileBytes {
		return nil, ErrTooLarge
	}
	defer func() {
		if r := recover(); r != nil {
			a, err = nil, fmt.Errorf("%w: %s: the decoder failed", ErrInvalidFile, format)
		}
	}()
	switch format {
	case FormatFIT:
		a, err = decodeFIT(data)
	case FormatGPX:
		a, err = decodeGPX(data)
	case FormatTCX:
		a, err = decodeTCX(data)
	default:
		return nil, fmt.Errorf("%w: unknown format", ErrInvalidLine)
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	a.format = format
	a.sha = hex.EncodeToString(sum[:])
	if err := a.finish(); err != nil {
		return nil, err
	}
	return a, nil
}

// finish sorts the points, drops sessions with no usable start, and checks
// that something is left.
func (a *activity) finish() error {
	sortPoints(a.points)
	kept := a.sessions[:0]
	for _, s := range a.sessions {
		if s.start.Before(minStart) {
			continue
		}
		if s.end.Before(s.start) {
			s.end = s.start
		}
		kept = append(kept, s)
	}
	a.sessions = kept
	if len(a.sessions) == 0 {
		return fmt.Errorf("%w: no workout with a start time", ErrNotActivity)
	}
	return nil
}

// assign sets each point's session: the session whose span holds it, or one
// that ends or starts within slack of it. Sessions are sorted by start.
func (a *activity) assign(slack time.Duration) {
	sort.SliceStable(a.sessions, func(i, j int) bool { return a.sessions[i].start.Before(a.sessions[j].start) })
	for i := range a.points {
		p := &a.points[i]
		p.sess = -1
		best := time.Duration(math.MaxInt64)
		for j, s := range a.sessions {
			var d time.Duration
			switch {
			case p.t.Before(s.start):
				d = s.start.Sub(p.t)
			case p.t.After(s.end):
				d = p.t.Sub(s.end)
			}
			if d <= slack && d < best {
				best, p.sess = d, j
			}
			if d == 0 {
				break
			}
		}
	}
}

// start is the earliest session start.
func (a *activity) start() time.Time {
	var t time.Time
	for _, s := range a.sessions {
		if t.IsZero() || s.start.Before(t) {
			t = s.start
		}
	}
	return t.UTC()
}

// info counts what Map stores of the file.
func (a *activity) info() Info {
	res := a.mapResult("")
	in := Info{Format: a.format, Start: a.start(), Workouts: len(res.Workouts), RoutePoints: len(res.RoutePoints)}
	for _, s := range res.QuantitySamples {
		if s.QuantityType == model.QTHeartRate {
			in.HeartRate++
		} else {
			in.Power++
		}
	}
	return in
}

// haversine is the great circle distance in meters between two fixes.
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371008.8 // mean Earth radius, m
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}

// summarize fills a GPX or TCX session's missing figures from its points:
// end, elapsed time, distance along the fixes, average and maximum heart
// rate and power, average speed. FIT sessions state their own.
func (s *session) summarize(pts []point) {
	var (
		dist         float64
		last         *point
		hrSum, pwSum float64
		hrN, pwN     int
		maxHR, maxPw = math.NaN(), math.NaN()
		first, lastT time.Time
	)
	for i := range pts {
		p := &pts[i]
		if first.IsZero() {
			first = p.t
		}
		lastT = p.t
		if p.hasPos() {
			if last != nil {
				dist += haversine(last.lat, last.lon, p.lat, p.lon)
			}
			last = p
		}
		if !math.IsNaN(p.hr) {
			hrSum += p.hr
			hrN++
			if math.IsNaN(maxHR) || p.hr > maxHR {
				maxHR = p.hr
			}
		}
		if !math.IsNaN(p.power) {
			pwSum += p.power
			pwN++
			if math.IsNaN(maxPw) || p.power > maxPw {
				maxPw = p.power
			}
		}
	}
	if s.start.IsZero() {
		s.start = first
	}
	if lastT.After(s.end) {
		s.end = lastT
	}
	if math.IsNaN(s.elapsedS) && !s.start.IsZero() && s.end.After(s.start) {
		s.elapsedS = s.end.Sub(s.start).Seconds()
	}
	if math.IsNaN(s.distanceM) && last != nil && dist > 0 {
		s.distanceM = dist
	}
	if math.IsNaN(s.avgHR) && hrN > 0 {
		s.avgHR = hrSum / float64(hrN)
	}
	if math.IsNaN(s.maxHR) {
		s.maxHR = maxHR
	}
	if math.IsNaN(s.avgPower) && pwN > 0 {
		s.avgPower = pwSum / float64(pwN)
	}
	if math.IsNaN(s.maxPower) {
		s.maxPower = maxPw
	}
	if math.IsNaN(s.avgSpeed) && !math.IsNaN(s.distanceM) {
		d := s.timerS
		if math.IsNaN(d) {
			d = s.elapsedS
		}
		if d > 0 {
			s.avgSpeed = s.distanceM / d
		}
	}
}
