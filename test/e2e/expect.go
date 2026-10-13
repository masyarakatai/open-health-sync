// SPDX-License-Identifier: AGPL-3.0-only

// Command expect computes, straight from an Apple Health fixture, the values
// the end to end suites assert once the server has processed it. It reads the
// NDJSON lines the way a reader of the SDK format would, without the
// connector or the ETL, so a mapping bug cannot hide behind its own
// expectation.
//
//	go run ./test/e2e/expect.go <dir>
//	    prints shell assignments: PARTS, HR_COUNT, STEP_COUNT, STEP_SUM,
//	    SPO2_COUNT, SLEEP_SESSIONS, WORKOUTS
//	go run ./test/e2e/expect.go gen <dir> <parts> <lines>
//	    writes <parts> parts of <lines> lines each, heart rate and step count
//	    in turn, every line distinct (the reliability volume)
//
// A part is a file named <part>_<HealthKit type>.ndjson; the suites upload it
// with X-OHS-Part and X-OHS-Type taken from that name.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	typeHeartRate = "HKQuantityTypeIdentifierHeartRate"
	typeSteps     = "HKQuantityTypeIdentifierStepCount"
	typeSpO2      = "HKQuantityTypeIdentifierOxygenSaturation"
	typeSleep     = "HKCategoryTypeIdentifierSleepAnalysis"
	sleepInBed    = 0
)

// Expectation is what the server must hold after processing a fixture.
type Expectation struct {
	Parts         int
	HeartRates    int
	Steps         int
	StepSum       float64
	SpO2          int
	SleepSessions int
	Workouts      int
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "expect:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	switch {
	case len(args) == 1:
		e, err := Compute(args[0])
		if err != nil {
			return err
		}
		return e.WriteShell(out)
	case len(args) == 4 && args[0] == "gen":
		parts, err1 := strconv.Atoi(args[2])
		lines, err2 := strconv.Atoi(args[3])
		if err1 != nil || err2 != nil || parts < 1 || lines < 1 {
			return errors.New("gen wants a positive number of parts and lines")
		}
		return Generate(args[1], parts, lines)
	default:
		return errors.New("usage: expect <dir> | expect gen <dir> <parts> <lines>")
	}
}

// Parts lists the fixture's part files, by part number.
func Parts(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.ndjson"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no *.ndjson part in %s", dir)
	}
	num := func(f string) int {
		n, _ := strconv.Atoi(strings.SplitN(filepath.Base(f), "_", 2)[0])
		return n
	}
	for _, f := range files {
		base := filepath.Base(f)
		head, typ, ok := strings.Cut(strings.TrimSuffix(base, ".ndjson"), "_")
		if _, err := strconv.Atoi(head); !ok || err != nil || typ == "" {
			return nil, fmt.Errorf("%s: want <part>_<type>.ndjson", base)
		}
	}
	sort.Slice(files, func(i, j int) bool { return num(files[i]) < num(files[j]) })
	return files, nil
}

// Compute reads every part of dir. A line counts once however often it
// appears: the server deduplicates identical records.
func Compute(dir string) (Expectation, error) {
	files, err := Parts(dir)
	if err != nil {
		return Expectation{}, err
	}
	e := Expectation{Parts: len(files)}
	seen := map[string]bool{}
	for _, f := range files {
		if err := eachLine(f, func(rec map[string]any) error {
			key, err := identity(rec)
			if err != nil {
				return err
			}
			if seen[key] {
				return nil
			}
			seen[key] = true
			switch {
			case rec["quantityType"] == typeHeartRate:
				e.HeartRates++
			case rec["quantityType"] == typeSteps:
				v, ok := rec["value"].(float64)
				if !ok {
					return errors.New("a step count line has no numeric value")
				}
				e.Steps++
				e.StepSum += v
			case rec["quantityType"] == typeSpO2:
				e.SpO2++
			case rec["categoryType"] == typeSleep:
				if v, _ := rec["value"].(float64); int(v) == sleepInBed {
					e.SleepSessions++
				}
			case rec["workoutActivityType"] != nil:
				e.Workouts++
			}
			return nil
		}); err != nil {
			return Expectation{}, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
	}
	return e, nil
}

// identity is what makes two lines the same record: kind, type, time span,
// value and source.
func identity(rec map[string]any) (string, error) {
	start, _ := rec["startDate"].(string)
	end, _ := rec["endDate"].(string)
	if _, err := time.Parse(time.RFC3339Nano, start); err != nil {
		return "", fmt.Errorf("startDate %q: %w", start, err)
	}
	kind := fmt.Sprint(rec["quantityType"], "|", rec["categoryType"], "|", rec["workoutActivityType"])
	return fmt.Sprint(kind, "|", start, "|", end, "|", rec["value"], "|", rec["sourceName"]), nil
}

func eachLine(path string, fn func(map[string]any) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		if err := fn(rec); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
	}
	return sc.Err()
}

// WriteShell prints the expectation as shell assignments. STEP_SUM keeps three
// decimals; the suites compare it with a tolerance.
func (e Expectation) WriteShell(w io.Writer) error {
	_, err := fmt.Fprintf(w, "PARTS=%d\nHR_COUNT=%d\nSTEP_COUNT=%d\nSTEP_SUM=%.3f\nSPO2_COUNT=%d\nSLEEP_SESSIONS=%d\nWORKOUTS=%d\n",
		e.Parts, e.HeartRates, e.Steps, e.StepSum, e.SpO2, e.SleepSessions, e.Workouts)
	return err
}

// Generate writes parts of distinct lines one second apart, heart rate and
// step count in turn, starting at a fixed instant so two runs write the same
// bytes.
func Generate(dir string, parts, lines int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	t := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for p := range parts {
		typ, unit := typeHeartRate, "count/min"
		if p%2 == 1 {
			typ, unit = typeSteps, "count"
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%d_%s.ndjson", p, typ)))
		if err != nil {
			return err
		}
		bw := bufio.NewWriter(f)
		enc := json.NewEncoder(bw)
		for i := range lines {
			value := float64(50 + (i*37)%90)
			if typ == typeSteps {
				value = float64(1 + (i*53)%40)
			}
			start := t.Format("2006-01-02T15:04:05.000Z")
			if err := enc.Encode(map[string]any{
				"device": "Apple Watch", "deviceModel": "Watch", "productType": "Watch7,1",
				"sourceBundleId": "com.apple.health.E2E", "sourceName": "Apple Watch",
				"wasUserEntered": false, "metadata": map[string]any{"HKTimeZone": "Europe/Paris"},
				"quantityType": typ, "unit": unit, "value": value,
				"startDate": start, "endDate": start,
			}); err != nil {
				return errors.Join(err, f.Close())
			}
			t = t.Add(time.Second)
		}
		if err := bw.Flush(); err != nil {
			return errors.Join(err, f.Close())
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}
