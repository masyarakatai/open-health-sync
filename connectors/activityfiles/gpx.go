// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"encoding/xml"
	"fmt"
	"time"
)

// gpxParser reads a GPX 1.0 or 1.1 file: the track points of every track,
// with the heart rate, power and speed of the Garmin TrackPointExtension (v1
// and v2) and the power element Strava and others write. Routes and
// waypoints carry no times and are ignored.
type gpxParser struct {
	creator string
	trkType string
	points  []point
	cur     *point
}

func (g *gpxParser) start(path []string, attrs []xml.Attr) {
	switch name := path[len(path)-1]; {
	case name == "gpx" && len(path) == 1:
		g.creator = attr(attrs, "creator")
	case name == "trkpt":
		p := newPoint(time.Time{})
		p.lat, p.lon = num(attr(attrs, "lat")), num(attr(attrs, "lon"))
		g.cur = &p
	}
}

func (g *gpxParser) end(path []string, text string) {
	name := path[len(path)-1]
	if name == "trkpt" {
		if g.cur != nil && !g.cur.t.IsZero() {
			g.points = append(g.points, *g.cur)
		}
		g.cur = nil
		return
	}
	if g.cur == nil {
		if name == "type" && parent(path) == "trk" && g.trkType == "" {
			g.trkType = text
		}
		return
	}
	switch name {
	case "time":
		if t, ok := xmlTime(text); ok && parent(path) == "trkpt" {
			g.cur.t = t
		}
	case "ele":
		if v := num(text); v >= -500 && v <= 9000 {
			g.cur.alt = v
		}
	case "hr", "heartrate":
		if within(path, "extensions") {
			g.cur.hr = num(text)
		}
	case "power", "PowerInWatts":
		if within(path, "extensions") {
			g.cur.power = num(text)
		}
	case "speed":
		if v := num(text); within(path, "extensions") && v >= 0 && v < 100 {
			g.cur.speed = v
		}
	}
}

// decodeGPX parses a GPX file into one workout over all its timed track
// points.
func decodeGPX(data []byte) (*activity, error) {
	g := &gpxParser{}
	if err := walkXML("GPX", data, g); err != nil {
		return nil, err
	}
	if len(g.points) == 0 {
		return nil, fmt.Errorf("%w: GPX file without timed track points", ErrNotActivity)
	}
	s := newSession()
	s.sport = g.trkType
	s.activity, s.indoor, s.virtual = textActivity(g.trkType)
	if g.trkType == "" {
		s.activity = "other"
	}
	s.power = powerQuantity(s.activity)
	a := &activity{src: textSource(g.creator), points: g.points}
	sortPoints(a.points)
	for i := range a.points {
		a.points[i].sess = 0
	}
	s.summarize(a.points)
	a.sessions = []session{s}
	return a, nil
}
