// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"strings"

	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// source is who recorded a file: the vendor (source slug), its display name
// and the device.
type source struct {
	slug        string // source slug; Slug when unknown
	name        string // display name of the vendor ("Garmin"), "" when unknown
	deviceModel string // product ("edge_530", "Forerunner 965")
	deviceID    string // vendor and product ("Garmin edge_530")
}

// fitVendors maps a FIT manufacturer name to the source slug when the two
// differ. Other manufacturers keep their FIT name ("hammerhead", "bryton").
var fitVendors = map[string]string{
	"wahoo_fitness":  "wahoo",
	"polar_electro":  "polar",
	"stages_cycling": "stages",
}

// vendorNames are the display names of the vendors most files come from.
var vendorNames = map[string]string{
	"garmin":     "Garmin",
	"coros":      "COROS",
	"wahoo":      "Wahoo",
	"suunto":     "Suunto",
	"polar":      "Polar",
	"zwift":      "Zwift",
	"strava":     "Strava",
	"hammerhead": "Hammerhead",
	"bryton":     "Bryton",
	"stages":     "Stages",
}

// fitSource names the vendor of a FIT file from its file_id manufacturer
// name, as the FIT profile spells it. "development", an unknown number and
// an empty name give the connector's own slug.
func fitSource(manufacturer string) source {
	m := model.Slug(manufacturer)
	if m == "" || m == "development" || strings.Contains(m, "invalid") || strings.ContainsAny(m, "()") {
		return source{slug: Slug}
	}
	if v, ok := fitVendors[m]; ok {
		m = v
	}
	return source{slug: m, name: vendorNames[m]}
}

// withDevice sets the device: model is the product, and the device id is the
// vendor's display name and the model, unless the model already starts with
// it.
func (s source) withDevice(model string) source {
	model = strings.TrimSpace(model)
	if model == "" {
		return s
	}
	s.deviceModel = model
	switch {
	case s.name == "":
		s.deviceID = model
	case strings.HasPrefix(strings.ToLower(model), strings.ToLower(s.name)):
		s.deviceID = model
	default:
		s.deviceID = s.name + " " + model
	}
	return s
}

// textVendors are the vendor names searched for, in order, in the creator,
// author and device names of a GPX or TCX file.
var textVendors = []struct{ word, slug string }{
	{"garmin", "garmin"},
	{"coros", "coros"},
	{"wahoo", "wahoo"},
	{"elemnt", "wahoo"},
	{"suunto", "suunto"},
	{"polar", "polar"},
	{"zwift", "zwift"},
	{"strava", "strava"},
	{"hammerhead", "hammerhead"},
	{"karoo", "hammerhead"},
	{"bryton", "bryton"},
}

// garminFamilies are Garmin product lines a TCX device name may give without
// the vendor's name ("Forerunner 965").
var garminFamilies = map[string]bool{
	"forerunner": true, "fenix": true, "edge": true, "venu": true, "vivoactive": true,
	"epix": true, "instinct": true, "enduro": true, "approach": true, "descent": true,
}

// textSource names the vendor of a GPX or TCX file from the names it
// carries, most specific first (the device, then the writing application).
// A file that names no known vendor gets the connector's own slug.
func textSource(names ...string) source {
	for _, n := range names {
		low := strings.ToLower(n)
		if low == "" {
			continue
		}
		for _, v := range textVendors {
			if strings.Contains(low, v.word) {
				return source{slug: v.slug, name: vendorNames[v.slug]}
			}
		}
		for _, w := range strings.FieldsFunc(low, func(r rune) bool {
			return (r < 'a' || r > 'z') && r != 0x113 // U+0113 (e with macron), as Garmin spells fenix
		}) {
			if garminFamilies[strings.ReplaceAll(w, "\u0113", "e")] {
				return source{slug: "garmin", name: vendorNames["garmin"]}
			}
		}
	}
	return source{slug: Slug}
}
