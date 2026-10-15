// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"strings"

	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// Power quantity types, by the sport that produced the reading.
const (
	qtCyclingPower = "cycling_power"
	qtRunningPower = "running_power"
)

// fitSports maps a FIT sport name to the canonical activity type. Sports not
// listed are "other"; the FIT name stays in the workout's metadata.
var fitSports = map[string]string{
	"running":                 "running",
	"cycling":                 "cycling",
	"e_biking":                "cycling",
	"swimming":                "swimming",
	"walking":                 "walking",
	"hiking":                  "hiking",
	"mountaineering":          "hiking",
	"rowing":                  "rowing",
	"cross_country_skiing":    "cross_country_skiing",
	"alpine_skiing":           "downhill_skiing",
	"snowboarding":            "snowboarding",
	"snowshoeing":             "snow_sports",
	"paddling":                "paddle_sports",
	"kayaking":                "paddle_sports",
	"rafting":                 "paddle_sports",
	"stand_up_paddleboarding": "paddle_sports",
	"surfing":                 "surfing_sports",
	"windsurfing":             "surfing_sports",
	"kitesurfing":             "surfing_sports",
	"wakeboarding":            "surfing_sports",
	"wakesurfing":             "surfing_sports",
	"water_skiing":            "water_sports",
	"water_tubing":            "water_sports",
	"water_sport":             "water_sports",
	"snorkeling":              "water_sports",
	"sailing":                 "sailing",
	"inline_skating":          "skating_sports",
	"ice_skating":             "skating_sports",
	"rock_climbing":           "climbing",
	"floor_climbing":          "stair_climbing",
	"golf":                    "golf",
	"disc_golf":               "disc_sports",
	"soccer":                  "soccer",
	"tennis":                  "tennis",
	"basketball":              "basketball",
	"american_football":       "american_football",
	"baseball":                "baseball",
	"volleyball":              "volleyball",
	"cricket":                 "cricket",
	"rugby":                   "rugby",
	"hockey":                  "hockey",
	"lacrosse":                "lacrosse",
	"boxing":                  "boxing",
	"mixed_martial_arts":      "martial_arts",
	"hiit":                    "high_intensity_interval_training",
	"dance":                   "dance",
	"jump_rope":               "jump_rope",
	"meditation":              "mind_and_body",
	"mobility":                "flexibility",
	"wheelchair_push_walk":    "wheelchair_walk_pace",
	"wheelchair_push_run":     "wheelchair_run_pace",
	"horseback_riding":        "equestrian_sports",
	"fishing":                 "fishing",
	"hunting":                 "hunting",
	"archery":                 "archery",
	"diving":                  "underwater_diving",
}

// fitSubSports refine a sport by its sub sport: a FIT "training" or
// "fitness_equipment" session says what it was only there.
var fitSubSports = map[string]string{
	"treadmill":            "running",
	"indoor_running":       "running",
	"indoor_cycling":       "cycling",
	"spin":                 "cycling",
	"hand_cycling":         "hand_cycling",
	"indoor_hand_cycling":  "hand_cycling",
	"indoor_rowing":        "rowing",
	"elliptical":           "elliptical",
	"stair_climbing":       "stair_climbing",
	"indoor_skiing":        "cross_country_skiing",
	"indoor_walking":       "walking",
	"strength_training":    "traditional_strength_training",
	"cardio_training":      "mixed_cardio",
	"flexibility_training": "flexibility",
	"yoga":                 "yoga",
	"pilates":              "pilates",
	"hiit":                 "high_intensity_interval_training",
	"amrap":                "high_intensity_interval_training",
	"emom":                 "high_intensity_interval_training",
	"tabata":               "high_intensity_interval_training",
	"indoor_climbing":      "climbing",
	"bouldering":           "climbing",
	"pickleball":           "pickleball",
	"squash":               "squash",
	"badminton":            "badminton",
	"racquetball":          "racquetball",
	"table_tennis":         "table_tennis",
}

// indoorSubSports are the FIT sub sports recorded indoors.
var indoorSubSports = map[string]bool{
	"treadmill": true, "indoor_running": true, "indoor_cycling": true, "spin": true,
	"indoor_rowing": true, "indoor_skiing": true, "indoor_walking": true,
	"indoor_hand_cycling": true, "indoor_climbing": true, "virtual_activity": true,
	"indoor_wheelchair_walk": true, "indoor_wheelchair_run": true,
}

// fitActivity returns the canonical activity type of a FIT sport and sub
// sport, and whether the session was indoors and virtual.
func fitActivity(sport, subSport string) (activity string, indoor, virtual bool) {
	activity = "other"
	if a, ok := fitSports[sport]; ok {
		activity = a
	}
	// A sub sport refines a generic sport, and names the sport a cycling or
	// running session of a fitness machine was (a hand cycle, a treadmill).
	if a, ok := fitSubSports[subSport]; ok {
		switch sport {
		case "running", "cycling", "walking", "rowing":
			if a == "hand_cycling" {
				activity = a
			}
		default:
			activity = a
		}
	}
	virtual = subSport == "virtual_activity"
	indoor = indoorSubSports[subSport]
	return activity, indoor, virtual
}

// powerQuantity is the quantity type of a session's power readings: cycling
// power for the cycling family, running power for running, none for the
// rest (no canonical quantity holds rowing or skiing power).
func powerQuantity(activity string) string {
	switch activity {
	case "cycling", "hand_cycling":
		return qtCyclingPower
	case "running":
		return qtRunningPower
	}
	return ""
}

// textSports maps the sport names GPX and TCX files carry (TCX Sport, GPX
// track type, lower cased) to the canonical activity type.
var textSports = map[string]string{
	"running":              "running",
	"run":                  "running",
	"trail_running":        "running",
	"treadmill":            "running",
	"virtualrun":           "running",
	"biking":               "cycling",
	"cycling":              "cycling",
	"ride":                 "cycling",
	"road_biking":          "cycling",
	"mountain_biking":      "cycling",
	"gravel_cycling":       "cycling",
	"indoor_cycling":       "cycling",
	"virtual_ride":         "cycling",
	"virtualride":          "cycling",
	"e_bike_ride":          "cycling",
	"walking":              "walking",
	"walk":                 "walking",
	"hiking":               "hiking",
	"hike":                 "hiking",
	"swimming":             "swimming",
	"swim":                 "swimming",
	"open_water":           "swimming",
	"rowing":               "rowing",
	"cross_country_skiing": "cross_country_skiing",
	"nordicski":            "cross_country_skiing",
	"alpine_skiing":        "downhill_skiing",
	"alpineski":            "downhill_skiing",
	"snowboarding":         "snowboarding",
	"snowboard":            "snowboarding",
	"kayaking":             "paddle_sports",
	"paddling":             "paddle_sports",
	"inline_skating":       "skating_sports",
	"other":                "other",
}

// textVirtual are the GPX and TCX sport names of virtual activities.
var textVirtual = map[string]bool{"virtual_ride": true, "virtualride": true, "virtualrun": true}

// textIndoor are the GPX and TCX sport names of indoor activities.
var textIndoor = map[string]bool{"treadmill": true, "indoor_cycling": true, "virtual_ride": true, "virtualride": true, "virtualrun": true}

// textActivity returns the canonical activity type of a TCX Sport or a GPX
// track type, and whether it was indoors and virtual.
func textActivity(name string) (activity string, indoor, virtual bool) {
	k := strings.ReplaceAll(model.Slug(name), " ", "_")
	if a, ok := textSports[k]; ok {
		return a, textIndoor[k], textVirtual[k]
	}
	return "other", false, false
}
