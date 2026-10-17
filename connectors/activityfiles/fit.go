// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"
)

// fitSlack is how far outside a FIT session a record may fall and still
// belong to it: devices write the session's last record a second or two
// after its end.
const fitSlack = 60 * time.Second

// fitListener collects what the mapping needs as the decoder streams the
// messages; nothing else of the file is kept.
type fitListener struct {
	fileType  typedef.File
	hasFileID bool
	maker     typedef.Manufacturer
	product   uint16
	prodName  string
	creator   string // product name of the creator device_info

	sessions []session
	points   []point

	sport, subSport string // the sport message, for a file without sessions

	actTime, actLocal time.Time
}

// OnMesg is called by the decoder for every message.
func (l *fitListener) OnMesg(m proto.Message) {
	switch m.Num {
	case mesgnum.FileId:
		if l.hasFileID {
			return // a chained file: the first file_id names the device
		}
		f := mesgdef.NewFileId(&m)
		l.hasFileID = true
		l.fileType = f.Type
		l.maker = f.Manufacturer
		l.product = f.Product
		l.prodName = strings.Clone(f.ProductName)
	case mesgnum.DeviceInfo:
		d := mesgdef.NewDeviceInfo(&m)
		if d.DeviceIndex == typedef.DeviceIndexCreator && l.creator == "" {
			l.creator = strings.Clone(d.ProductName)
		}
	case mesgnum.Sport:
		s := mesgdef.NewSport(&m)
		if l.sport == "" {
			l.sport, l.subSport = fitName(s.Sport.String()), fitName(s.SubSport.String())
		}
	case mesgnum.Activity:
		a := mesgdef.NewActivity(&m)
		if l.actTime.IsZero() {
			l.actTime, l.actLocal = a.Timestamp, a.LocalTimestamp
		}
	case mesgnum.Session:
		l.sessions = append(l.sessions, fitSession(mesgdef.NewSession(&m)))
	case mesgnum.Record:
		if p, ok := fitPoint(mesgdef.NewRecord(&m)); ok {
			l.points = append(l.points, p)
		}
	}
}

// decodeFIT decodes a FIT file, chained files included, checking every CRC.
func decodeFIT(data []byte) (*activity, error) {
	l := &fitListener{}
	dec := decoder.New(bytes.NewReader(data), decoder.WithMesgListener(l), decoder.WithBroadcastOnly())
	files := 0
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			return nil, fitError(err)
		}
		files++
	}
	if files == 0 {
		return nil, fmt.Errorf("%w: not a FIT file", ErrInvalidFile)
	}
	if !l.hasFileID {
		return nil, fmt.Errorf("%w: FIT file without file_id", ErrNotActivity)
	}
	if l.fileType != typedef.FileActivity {
		return nil, fmt.Errorf("%w: FIT file of type %s", ErrNotActivity, fitTypeName(l.fileType))
	}

	a := &activity{points: l.points}
	a.src = fitSource(l.maker.String()).withDevice(l.deviceModel())
	if off, ok := fitOffset(l.actTime, l.actLocal); ok {
		a.offset, a.hasOffset = off, true
	}
	a.sessions = l.sessions
	if len(a.sessions) == 0 && len(a.points) > 0 {
		// Some apps write records without a session: one workout over them.
		s := newSession()
		s.sport, s.subSport = l.sport, l.subSport
		s.activity, s.indoor, s.virtual = fitActivity(s.sport, s.subSport)
		s.power = powerQuantity(s.activity)
		s.summarize(a.points)
		a.sessions = []session{s}
	}
	for i := range a.sessions {
		s := &a.sessions[i]
		if s.start.IsZero() || s.start.Before(minStart) {
			s.start = firstPointAfter(a.points, minStart)
		}
		if s.end.Before(s.start) {
			s.end = s.start
		}
	}
	a.assign(fitSlack)
	return a, nil
}

// fitError turns a decoder error into ErrInvalidFile with a fixed message.
func fitError(err error) error {
	switch {
	case errors.Is(err, decoder.ErrNotFITFile):
		return fmt.Errorf("%w: not a FIT file", ErrInvalidFile)
	case errors.Is(err, decoder.ErrCRCChecksumMismatch):
		return fmt.Errorf("%w: FIT CRC mismatch", ErrInvalidFile)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%w: FIT file truncated", ErrInvalidFile)
	default:
		return fmt.Errorf("%w: FIT data does not decode", ErrInvalidFile)
	}
}

// fitName is a FIT profile name, "" for a value the profile does not name
// (the library spells those "SportInvalid(n)").
func fitName(s string) string {
	if strings.ContainsAny(s, "()") {
		return ""
	}
	return s
}

// fitTypeName names a FIT file type without echoing an unknown number's
// formatting.
func fitTypeName(t typedef.File) string {
	if s := t.String(); !strings.ContainsAny(s, "()") {
		return s
	}
	return strconv.Itoa(int(t))
}

// deviceModel is the product of the file: the Garmin product name for a
// Garmin device, else the product name the file carries, else the product
// number.
func (l *fitListener) deviceModel() string {
	switch l.maker {
	case typedef.ManufacturerGarmin, typedef.ManufacturerDynastream, typedef.ManufacturerDynastreamOem, typedef.ManufacturerTacx:
		if l.product != basetype.Uint16Invalid {
			if s := typedef.GarminProduct(l.product).String(); !strings.ContainsAny(s, "()") {
				return s
			}
		}
	}
	if n := strings.TrimSpace(l.prodName); n != "" {
		return n
	}
	if n := strings.TrimSpace(l.creator); n != "" {
		return n
	}
	if l.product != basetype.Uint16Invalid && l.product != 0 && l.maker != typedef.ManufacturerDevelopment {
		return "product_" + strconv.Itoa(int(l.product))
	}
	return ""
}

// fitOffset is the UTC offset of the activity message, in minutes: its local
// timestamp minus its timestamp. Accepted when within UTC-12 to UTC+14 and a
// multiple of 15 minutes.
func fitOffset(t, local time.Time) (int, bool) {
	if t.IsZero() || local.IsZero() {
		return 0, false
	}
	d := local.Sub(t)
	if d < -12*time.Hour || d > 14*time.Hour || d%(15*time.Minute) != 0 {
		return 0, false
	}
	return int(d / time.Minute), true
}

// firstPointAfter is the time of the first point at or after t, zero if none.
func firstPointAfter(pts []point, t time.Time) time.Time {
	var first time.Time
	for _, p := range pts {
		if !p.t.Before(t) && (first.IsZero() || p.t.Before(first)) {
			first = p.t
		}
	}
	return first
}

// fitSession reads one session message.
func fitSession(m *mesgdef.Session) session {
	s := newSession()
	s.sport, s.subSport = fitName(m.Sport.String()), fitName(m.SubSport.String())
	s.activity, s.indoor, s.virtual = fitActivity(s.sport, s.subSport)
	s.power = powerQuantity(s.activity)
	s.start = m.StartTime.UTC()

	s.elapsedS = nonNeg(m.TotalElapsedTimeScaled())
	s.timerS = nonNeg(m.TotalTimerTimeScaled())
	switch {
	case !s.start.IsZero() && !math.IsNaN(s.elapsedS):
		s.end = s.start.Add(time.Duration(s.elapsedS * float64(time.Second)))
	case !m.Timestamp.IsZero():
		s.end = m.Timestamp.UTC()
	case !s.start.IsZero() && !math.IsNaN(s.timerS):
		s.end = s.start.Add(time.Duration(s.timerS * float64(time.Second)))
	default:
		s.end = s.start
	}

	s.distanceM = positive(m.TotalDistanceScaled())
	s.kcal = u16(m.TotalCalories)
	s.avgHR = u8(m.AvgHeartRate)
	s.maxHR = u8(m.MaxHeartRate)
	s.ascentM = u16zero(m.TotalAscent)
	s.descentM = u16zero(m.TotalDescent)
	s.avgSpeed = firstValid(m.EnhancedAvgSpeedScaled(), m.AvgSpeedScaled())
	s.maxSpeed = firstValid(m.EnhancedMaxSpeedScaled(), m.MaxSpeedScaled())
	s.avgPower = u16(m.AvgPower)
	s.maxPower = u16(m.MaxPower)
	s.normPower = u16(m.NormalizedPower)
	s.avgCadence = u8(m.AvgCadence)
	s.maxCadence = u8(m.MaxCadence)
	return s
}

// fitPoint reads one record message. A record with no time is dropped.
func fitPoint(r *mesgdef.Record) (point, bool) {
	if r.Timestamp.IsZero() || r.Timestamp.Before(minStart) {
		return point{}, false
	}
	p := newPoint(r.Timestamp.UTC())
	if r.PositionLat != basetype.Sint32Invalid && r.PositionLong != basetype.Sint32Invalid {
		p.lat, p.lon = r.PositionLatDegrees(), r.PositionLongDegrees()
	}
	if alt := firstValid(r.EnhancedAltitudeScaled(), r.AltitudeScaled()); alt >= -500 && alt <= 9000 {
		p.alt = alt
	}
	if v := firstValid(r.EnhancedSpeedScaled(), r.SpeedScaled()); v >= 0 && v < 100 {
		p.speed = v
	}
	if r.HeartRate != basetype.Uint8Invalid {
		p.hr = float64(r.HeartRate)
	}
	if r.Power != basetype.Uint16Invalid {
		p.power = float64(r.Power)
	}
	return p, true
}

// firstValid returns the first figure that is not NaN, or NaN.
func firstValid(vs ...float64) float64 {
	for _, v := range vs {
		if !math.IsNaN(v) {
			return v
		}
	}
	return math.NaN()
}

func nonNeg(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return math.NaN()
	}
	return v
}

func positive(v float64) float64 {
	if math.IsNaN(v) || v <= 0 {
		return math.NaN()
	}
	return v
}

// u8 is a uint8 figure, NaN when invalid or zero (a device writes zero for a
// figure it did not measure).
func u8(v uint8) float64 {
	if v == basetype.Uint8Invalid || v == 0 {
		return math.NaN()
	}
	return float64(v)
}

// u16 is a uint16 figure, NaN when invalid or zero.
func u16(v uint16) float64 {
	if v == basetype.Uint16Invalid || v == 0 {
		return math.NaN()
	}
	return float64(v)
}

// u16zero is a uint16 figure where zero is a real value (a flat ride climbs
// zero meters), NaN when invalid.
func u16zero(v uint16) float64 {
	if v == basetype.Uint16Invalid {
		return math.NaN()
	}
	return float64(v)
}
