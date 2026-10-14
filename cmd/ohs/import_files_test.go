// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// activityTestdata holds the invented activity files of the connector's
// tests.
var activityTestdata = filepath.Join("..", "..", "connectors", "activityfiles", "testdata")

func activityFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(activityTestdata, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// activityExport lays out an invented export: files named like a Garmin
// export's (after an email address), a nested zip, a .gz, a file twice, a
// monitoring file, a broken file and a CSV.
func activityExport(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	put := func(name string, b []byte) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var inner bytes.Buffer
	zw := zip.NewWriter(&inner)
	for _, name := range []string{"garmin_ride.fit", "garmin_multisport.fit"} {
		w, err := zw.Create("someone@example.com_" + name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(activityFixture(t, name))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var outer bytes.Buffer
	zw = zip.NewWriter(&outer)
	w, _ := zw.Create("DI_CONNECT/DI-Connect-Uploaded-Files/UploadedFiles_0-_Part1.zip")
	_, _ = w.Write(inner.Bytes())
	w, _ = zw.Create("DI_CONNECT/DI-Connect-Wellness/sleep.json")
	_, _ = w.Write([]byte(`{"invented":true}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	put("garmin/export.zip", outer.Bytes())

	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	_, _ = gw.Write(activityFixture(t, "strava_ride.tcx"))
	_ = gw.Close()
	put("strava/activities/1001.tcx.gz", gz.Bytes())
	put("strava/activities/1002.gpx", activityFixture(t, "strava_ride.gpx"))
	put("strava/activities.csv", []byte("invented\n"))
	put("loose/someone@example.com_ride_copy.fit", activityFixture(t, "garmin_ride.fit"))
	put("loose/monitoring.fit", activityFixture(t, "monitoring.fit"))
	put("loose/broken.fit", activityFixture(t, "bad_crc.fit"))
	put("loose/run.gpx", activityFixture(t, "garmin_run.gpx"))
	return dir
}

// filesStub plays POST /v1/ingest/activity-files: it checks every part as
// the server would, and answers a part it already holds as a duplicate.
type filesStub struct {
	mu     sync.Mutex
	stored map[string]bool
	types  map[string]int
	refuse string // an X-OHS-Type answered with 400
	bad    []string
}

func newFilesStub(t *testing.T) (*filesStub, *httptest.Server) {
	s := &filesStub{stored: map[string]bool{}, types: map[string]int{}}
	batch := regexp.MustCompile(`^af1-[0-9a-f]{32}$`)
	day := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/v1/ingest/activity-files" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer ohs_ut_cli_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		typ := r.Header.Get("X-OHS-Type")
		if typ == s.refuse {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"title": "Bad Request", "detail": "activity_files: the file holds no activity"})
			return
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			s.bad = append(s.bad, "not gzip")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(zr)
		var l struct{ Type, Data string }
		if json.Unmarshal(body, &l) != nil || l.Type != typ || bytes.Count(body, []byte("\n")) != 1 {
			s.bad = append(s.bad, "bad line")
		}
		if _, err := base64.StdEncoding.DecodeString(l.Data); err != nil {
			s.bad = append(s.bad, "bad base64")
		}
		if !batch.MatchString(r.Header.Get("X-OHS-Batch")) || r.Header.Get("X-OHS-Part") != "0" ||
			!day.MatchString(r.Header.Get("X-OHS-Day")) {
			s.bad = append(s.bad, "bad headers")
		}
		key := r.Header.Get("X-OHS-Day") + "/" + r.Header.Get("X-OHS-Batch")
		dup := s.stored[key]
		s.stored[key] = true
		if !dup {
			s.types[typ]++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"key": key, "duplicate": dup})
	}))
	t.Cleanup(srv.Close)
	return s, srv
}

func TestImportFiles(t *testing.T) {
	stub, srv := newFilesStub(t)
	dir := activityExport(t)
	var out bytes.Buffer
	err := run([]string{"import", "files", dir, "--url", srv.URL, "--token", "ohs_ut_cli_test"}, &out)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	text := out.String()
	if len(stub.bad) > 0 {
		t.Fatalf("the stub saw malformed parts: %v", stub.bad)
	}
	// Six distinct activity files: two FIT in the nested zip, the TCX, two
	// GPX; the loose FIT copy is the same file as one in the zip.
	if got := stub.types; got["fit"] != 2 || got["gpx"] != 2 || got["tcx"] != 1 {
		t.Errorf("stored %v, want 2 fit, 2 gpx, 1 tcx", got)
	}
	for _, want := range []string{
		"activity files          5", "workouts                6", "stored now              5",
		"already stored          0", "same file twice         1", "zips opened             2",
		"other files             2", "skipped, not an activity:", "skipped, could not be read:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	// No token, no file or folder name, no health value in the output.
	values := strings.ReplaceAll(text, srv.URL, "")
	for _, never := range []string{"ohs_ut_cli_test", "example.com", "someone", "UploadedFiles", "1001", "monitoring.fit", "broken", "45.1", "124"} {
		if strings.Contains(values, never) {
			t.Errorf("output shows %q:\n%s", never, text)
		}
	}

	// A second run, and a run on another export holding the same files,
	// upload nothing new: every file is answered as already stored.
	out.Reset()
	if err := run([]string{"import", "files", dir, "--url", srv.URL, "--token", "ohs_ut_cli_test"}, &out); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if !strings.Contains(out.String(), "stored now              0") || !strings.Contains(out.String(), "already stored          5") {
		t.Errorf("second run should find every file stored:\n%s", out.String())
	}
	if got := stub.types; got["fit"] != 2 || got["gpx"] != 2 || got["tcx"] != 1 {
		t.Errorf("after the second run stored %v", got)
	}
	out.Reset()
	single := filepath.Join(t.TempDir(), "ride.fit")
	if err := os.WriteFile(single, activityFixture(t, "garmin_ride.fit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"import", "files", single, "--url", srv.URL, "--token", "ohs_ut_cli_test"}, &out); err != nil {
		t.Fatalf("single file: %v", err)
	}
	if !strings.Contains(out.String(), "already stored          1") {
		t.Errorf("a file of an earlier import is already stored:\n%s", out.String())
	}
}

func TestImportFilesSince(t *testing.T) {
	_, srv := newFilesStub(t)
	var out bytes.Buffer
	// The fixtures start from 2026-03-01 to 2026-03-05 (UTC).
	err := run([]string{"import", "files", activityExport(t), "--since", "2026-03-03", "--url", srv.URL, "--token", "ohs_ut_cli_test"}, &out)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "before --since          3") || !strings.Contains(out.String(), "stored now              2") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestImportFilesServerRefusal(t *testing.T) {
	stub, srv := newFilesStub(t)
	stub.refuse = "gpx"
	var out bytes.Buffer
	err := run([]string{"import", "files", activityExport(t), "--url", srv.URL, "--token", "ohs_ut_cli_test"}, &out)
	if err == nil || !strings.Contains(err.Error(), "refused 2 activity file(s)") {
		t.Fatalf("err = %v, want the refused count", err)
	}
	// The other files are stored, and the refusal is counted by reason.
	if !strings.Contains(out.String(), "stored now              3") ||
		!strings.Contains(out.String(), "HTTP 400: Bad Request: activity_files: the file holds no activity 2") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestImportFilesRefusedToken(t *testing.T) {
	_, srv := newFilesStub(t)
	err := run([]string{"import", "files", activityExport(t), "--url", srv.URL, "--token", "ohs_ut_wrong"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "ohs_ut_wrong") {
		t.Fatalf("err = %v, want a 401 that does not show the token", err)
	}
}

func TestImportFilesDryRunAndUsage(t *testing.T) {
	t.Setenv("OHS_URL", "")
	t.Setenv("OHS_TOKEN", "")
	var out bytes.Buffer
	if err := run([]string{"import", "files", activityExport(t), "--dry-run"}, &out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, want := range []string{"dry run done", "activity files          5", "    fit                                      2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "stored now") {
		t.Errorf("a dry run stores nothing:\n%s", out.String())
	}
	for want, args := range map[string][]string{
		"one activity file":    {"import", "files"},
		"--tz is for":          {"import", "files", "x.fit", "--dry-run", "--tz", "Europe/Paris"},
		"--batch is not for":   {"import", "files", "x.fit", "--dry-run", "--batch", "b1"},
		"YYYY-MM-DD":           {"import", "files", "x.fit", "--dry-run", "--since", "yesterday"},
		"--token":              {"import", "files", "x.fit", "--url", "https://ohs.example.org"},
		"mi-fitness and files": {"import", "garmin", "x.zip"},
	} {
		err := run(args, &bytes.Buffer{})
		var ue usageError
		if err == nil || !errors.As(err, &ue) || !strings.Contains(err.Error(), want) {
			t.Errorf("run %v = %v, want a usage error containing %q", args, err, want)
		}
	}
	err := run([]string{"import", "files", filepath.Join(t.TempDir(), "missing.zip"), "--dry-run"}, &bytes.Buffer{})
	if err == nil {
		t.Error("a missing path must fail")
	}
}
