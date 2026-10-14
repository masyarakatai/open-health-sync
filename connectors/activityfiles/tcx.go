// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"encoding/xml"
	"fmt"
	"math"
	"time"
)

// tcxActivity is one Activity element being read.
type tcxActivity struct {
	sport   string
	id      time.Time
	creator string // Creator/Name: the device
	laps    []tcxLap
	points  []point
}

// tcxLap holds the figures a lap states; the activity sums them.
type tcxLap struct {
	start              time.Time
	timeS, distM, kcal float64
	avgHR, maxHR       float64
	avgW, maxW         float64
	maxSpeed           float64
}

func newLap() tcxLap {
	return tcxLap{timeS: nan, distM: nan, kcal: nan, avgHR: nan, maxHR: nan, avgW: nan, maxW: nan, maxSpeed: nan}
}

// tcxParser reads a TCX file (Training Center Database v2): every Activity,
// its laps and track points, with the speed and power of ActivityExtension
// v2. Courses, workouts and multisport sessions are ignored.
type tcxParser struct {
	author string
	acts   []tcxActivity
	act    *tcxActivity
	lap    *tcxLap
	tp     *point
}

func (t *tcxParser) start(path []string, attrs []xml.Attr) {
	switch name := path[len(path)-1]; name {
	case "Activity":
		if parent(path) == "Activities" {
			t.act = &tcxActivity{sport: attr(attrs, "Sport")}
		}
	case "Lap":
		if t.act != nil {
			l := newLap()
			l.start, _ = xmlTime(attr(attrs, "StartTime"))
			t.lap = &l
		}
	case "Trackpoint":
		if t.act != nil {
			p := newPoint(time.Time{})
			t.tp = &p
		}
	}
}

func (t *tcxParser) end(path []string, text string) {
	name, up := path[len(path)-1], parent(path)
	switch {
	case name == "Activity" && t.act != nil && up == "Activities":
		t.acts = append(t.acts, *t.act)
		t.act = nil
	case t.act == nil:
		if name == "Name" && up == "Author" && t.author == "" {
			t.author = text
		}
	case name == "Trackpoint":
		if t.tp != nil && !t.tp.t.IsZero() {
			t.act.points = append(t.act.points, *t.tp)
		}
		t.tp = nil
	case t.tp != nil:
		t.trackpoint(name, up, text)
	case name == "Lap":
		if t.lap != nil {
			t.act.laps = append(t.act.laps, *t.lap)
		}
		t.lap = nil
	case t.lap != nil:
		t.lapField(name, up, text)
	case name == "Id" && up == "Activity":
		t.act.id, _ = xmlTime(text)
	case name == "Name" && up == "Creator":
		t.act.creator = text
	}
}

func (t *tcxParser) trackpoint(name, up, text string) {
	p := t.tp
	switch name {
	case "Time":
		if v, ok := xmlTime(text); ok {
			p.t = v
		}
	case "LatitudeDegrees":
		p.lat = num(text)
	case "LongitudeDegrees":
		p.lon = num(text)
	case "AltitudeMeters":
		if v := num(text); v >= -500 && v <= 9000 {
			p.alt = v
		}
	case "Value":
		if up == "HeartRateBpm" {
			p.hr = num(text)
		}
	case "Speed":
		if v := num(text); v >= 0 && v < 100 {
			p.speed = v
		}
	case "Watts":
		p.power = num(text)
	}
}

func (t *tcxParser) lapField(name, up, text string) {
	l := t.lap
	switch name {
	case "TotalTimeSeconds":
		l.timeS = num(text)
	case "DistanceMeters":
		if up == "Lap" {
			l.distM = num(text)
		}
	case "Calories":
		l.kcal = num(text)
	case "MaximumSpeed":
		l.maxSpeed = num(text)
	case "Value":
		switch up {
		case "AverageHeartRateBpm":
			l.avgHR = num(text)
		case "MaximumHeartRateBpm":
			l.maxHR = num(text)
		}
	case "AvgWatts":
		l.avgW = num(text)
	case "MaxWatts":
		l.maxW = num(text)
	}
}

// decodeTCX parses a TCX file into one workout per Activity.
func decodeTCX(data []byte) (*activity, error) {
	t := &tcxParser{}
	if err := walkXML("TCX", data, t); err != nil {
		return nil, err
	}
	a := &activity{}
	var device string
	for _, ta := range t.acts {
		s := tcxSession(ta)
		if s.start.IsZero() {
			continue
		}
		idx := len(a.sessions)
		for _, p := range ta.points {
			p.sess = idx
			a.points = append(a.points, p)
		}
		a.sessions = append(a.sessions, s)
		if device == "" {
			device = ta.creator
		}
	}
	if len(a.sessions) == 0 {
		return nil, fmt.Errorf("%w: TCX file without a timed activity", ErrNotActivity)
	}
	a.src = textSource(device, t.author).withDevice(device)
	return a, nil
}

// tcxSession sums an activity's laps and fills what they leave out from its
// track points.
func tcxSession(ta tcxActivity) session {
	s := newSession()
	s.sport = ta.sport
	s.activity, s.indoor, s.virtual = textActivity(ta.sport)
	s.power = powerQuantity(s.activity)
	s.start = ta.id
	sortPoints(ta.points)

	var hrWeighted, hrTime, wWeighted, wTime float64
	for _, l := range ta.laps {
		if s.start.IsZero() || (!l.start.IsZero() && l.start.Before(s.start)) {
			s.start = l.start
		}
		s.timerS = addFig(s.timerS, l.timeS)
		s.distanceM = addFig(s.distanceM, l.distM)
		s.kcal = addFig(s.kcal, l.kcal)
		s.maxHR = maxFig(s.maxHR, l.maxHR)
		s.maxPower = maxFig(s.maxPower, l.maxW)
		s.maxSpeed = maxFig(s.maxSpeed, l.maxSpeed)
		if !math.IsNaN(l.avgHR) && l.timeS > 0 {
			hrWeighted += l.avgHR * l.timeS
			hrTime += l.timeS
		}
		if !math.IsNaN(l.avgW) && l.timeS > 0 {
			wWeighted += l.avgW * l.timeS
			wTime += l.timeS
		}
		if !l.start.IsZero() && l.timeS > 0 {
			if e := l.start.Add(time.Duration(l.timeS * float64(time.Second))); e.After(s.end) {
				s.end = e
			}
		}
	}
	if hrTime > 0 {
		s.avgHR = hrWeighted / hrTime
	}
	if wTime > 0 {
		s.avgPower = wWeighted / wTime
	}
	if s.start.IsZero() && len(ta.points) > 0 {
		s.start = ta.points[0].t
	}
	if s.end.Before(s.start) {
		s.end = s.start
	}
	s.summarize(ta.points)
	if !math.IsNaN(s.kcal) && s.kcal <= 0 {
		s.kcal = nan
	}
	if !math.IsNaN(s.distanceM) && s.distanceM <= 0 {
		s.distanceM = nan
	}
	return s
}

// addFig adds b to a, either of which may be missing.
func addFig(a, b float64) float64 {
	switch {
	case math.IsNaN(b) || b < 0:
		return a
	case math.IsNaN(a):
		return b
	}
	return a + b
}

// maxFig is the larger of a and b, either of which may be missing.
func maxFig(a, b float64) float64 {
	if math.IsNaN(a) || (!math.IsNaN(b) && b > a) {
		return b
	}
	return a
}
