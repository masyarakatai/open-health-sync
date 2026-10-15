// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"math"
	"strconv"
	"time"

	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// Hard bounds of the readings kept. A reading outside them is a sensor
// placeholder (a FIT heart rate of 0 without a strap) or a fault; the ETL
// validator would reject it and park the whole file as a failed record.
const (
	hrFloor, hrCeiling = 20, 300
	cyclingWCeiling    = 5000
	runningWCeiling    = 3000
)

// mapResult turns a parsed file into canonical records for userID.
func (a *activity) mapResult(userID string) model.MapResult {
	res := model.MapResult{}
	zone := ""
	if a.hasOffset {
		zone = offsetZone(a.offset)
	}
	ref := "af-" + a.sha[:20]

	hashes := make([]string, len(a.sessions))
	for i := range a.sessions {
		w := a.workout(i, userID, ref, zone)
		hashes[i] = w.ContentHash
		res.Workouts = append(res.Workouts, w)
	}

	seenHR := map[int64]bool{}
	seenW := map[int64]bool{}
	for _, p := range a.points {
		var s *session
		if p.sess >= 0 && p.sess < len(a.sessions) {
			s = &a.sessions[p.sess]
		}
		if s != nil && !s.virtual && p.hasPos() {
			rp := model.RoutePoint{
				UserID:            userID,
				Timestamp:         p.t,
				Latitude:          p.lat,
				Longitude:         p.lon,
				ParentContentHash: hashes[p.sess],
			}
			if !math.IsNaN(p.alt) {
				alt := p.alt
				rp.AltitudeM = &alt
			}
			if !math.IsNaN(p.speed) {
				v := p.speed
				rp.SpeedMps = &v
			}
			res.RoutePoints = append(res.RoutePoints, rp)
		}
		key := p.t.UnixNano()
		if hr := p.hr; !math.IsNaN(hr) && hr >= hrFloor && hr <= hrCeiling && !seenHR[key] {
			seenHR[key] = true
			res.QuantitySamples = append(res.QuantitySamples, a.sample(userID, ref, zone, model.QTHeartRate, "bpm", hr, p.t))
		}
		if s == nil || s.power == "" || math.IsNaN(p.power) || p.power < 0 || seenW[key] {
			continue
		}
		limit := float64(cyclingWCeiling)
		if s.power == qtRunningPower {
			limit = runningWCeiling
		}
		if p.power <= limit {
			seenW[key] = true
			res.QuantitySamples = append(res.QuantitySamples, a.sample(userID, ref, zone, s.power, "W", p.power, p.t))
		}
	}
	res.HighFrequency = len(res.RoutePoints) > 0 || len(res.QuantitySamples) > 0
	return res
}

// workout builds the workout of session i.
func (a *activity) workout(i int, userID, ref, zone string) model.Workout {
	s := a.sessions[i]
	md := map[string]any{
		"file_format": a.format,
		"file_sha256": a.sha,
	}
	if len(a.sessions) > 1 {
		md["session_index"] = i
		md["sessions"] = len(a.sessions)
	}
	switch a.format {
	case FormatFIT:
		putText(md, "fit_sport", s.sport)
		putText(md, "fit_sub_sport", s.subSport)
	case FormatTCX:
		putText(md, "tcx_sport", s.sport)
	case FormatGPX:
		putText(md, "gpx_type", s.sport)
	}
	if s.indoor {
		md["indoor"] = true
	}
	if s.virtual {
		md["virtual"] = true
	}
	putText(md, "device_name", a.src.deviceModel)
	if a.hasOffset {
		md["utc_offset_minutes"] = a.offset
	}
	putText(md, "timezone", zone)
	putFig(md, "elapsed_time_s", s.elapsedS)
	putFig(md, "moving_time_s", s.timerS)
	putFig(md, "total_descent_m", s.descentM)
	putFig(md, "max_speed_mps", s.maxSpeed)
	putFig(md, "avg_power_w", s.avgPower)
	putFig(md, "max_power_w", s.maxPower)
	putFig(md, "normalized_power_w", s.normPower)
	putFig(md, "avg_cadence", s.avgCadence)
	putFig(md, "max_cadence", s.maxCadence)

	w := model.Workout{
		UserID:           userID,
		Provider:         Slug,
		SourceSlug:       a.src.slug,
		DeviceID:         a.src.deviceID,
		ExternalID:       ref + "-" + strconv.Itoa(i),
		ActivityType:     s.activity,
		StartDate:        s.start,
		EndDate:          s.end,
		ActiveEnergyKcal: fig(s.kcal),
		DistanceM:        fig(s.distanceM),
		AvgHeartRate:     fig(s.avgHR),
		MaxHeartRate:     fig(s.maxHR),
		AvgSpeedMps:      fig(s.avgSpeed),
		ElevationGainM:   fig(s.ascentM),
		Metadata:         md,
	}
	dur := s.timerS
	if math.IsNaN(dur) {
		dur = s.elapsedS
	}
	if !math.IsNaN(dur) {
		d := int(math.Round(dur))
		w.DurationS = &d
	}
	w.ContentHash = model.ComputeContentHash(map[string]string{
		"user_id":     userID,
		"provider":    Slug,
		"data_type":   "workout",
		"source_slug": a.src.slug,
		"device_id":   a.src.deviceID,
		"file_format": a.format,
		"start_date":  s.start.UTC().Truncate(time.Second).Format(time.RFC3339),
	})
	return w
}

// sample builds one reading. Readings are hashed on their source, type, time
// and value, so the copies of one reading in two files of the same vendor
// (a FIT and a GPX of one ride) are one row.
func (a *activity) sample(userID, ref, zone, qt, unit string, v float64, at time.Time) model.QuantitySample {
	s := model.QuantitySample{
		UserID:          userID,
		Provider:        Slug,
		SourceSlug:      a.src.slug,
		SourceName:      a.src.name,
		DeviceID:        a.src.deviceID,
		DeviceModel:     a.src.deviceModel,
		ExternalID:      ref,
		QuantityType:    qt,
		Value:           v,
		Unit:            unit,
		IsPrimarySource: true,
		StartDate:       at,
		EndDate:         at,
	}
	if zone != "" {
		// One map per sample: the validator writes its quality flag into it.
		s.Metadata = map[string]any{"timezone": zone}
	}
	s.ContentHash = model.ComputeContentHash(map[string]string{
		"user_id":       userID,
		"provider":      Slug,
		"source_slug":   a.src.slug,
		"quantity_type": qt,
		"start_date":    at.UTC().Format(time.RFC3339Nano),
		"value":         model.CanonicalFloat(v),
	})
	return s
}

// offsetZone is the Etc/GMT zone of a whole hour offset ("Etc/GMT-2" is
// UTC+2), "Etc/UTC" for 0, and "" otherwise.
func offsetZone(minutes int) string {
	if minutes == 0 {
		return "Etc/UTC"
	}
	if minutes%60 != 0 {
		return ""
	}
	h := minutes / 60
	if h > 0 {
		return "Etc/GMT-" + strconv.Itoa(h)
	}
	return "Etc/GMT+" + strconv.Itoa(-h)
}

// fig is a figure as an optional column value: nil when missing or negative.
func fig(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return nil
	}
	return &v
}

func putFig(md map[string]any, k string, v float64) {
	if p := fig(v); p != nil {
		md[k] = math.Round(*p*1000) / 1000
	}
}

// maxMetaText bounds a name copied from the file into metadata.
const maxMetaText = 128

func putText(md map[string]any, k, v string) {
	if v == "" {
		return
	}
	if r := []rune(v); len(r) > maxMetaText {
		v = string(r[:maxMetaText])
	}
	md[k] = v
}
