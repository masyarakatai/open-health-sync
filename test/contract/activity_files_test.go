// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package contract

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/connectors/activityfiles"
	"github.com/soma-intelligence/open-health-sync/internal/db"
	"github.com/soma-intelligence/open-health-sync/internal/tenant"
)

const activityDir = "../../connectors/activityfiles/testdata"

// TestActivityFilesMatchOpenAPI uploads activity files to
// POST /v1/ingest/activity-files, reads back what the ETL stored, and checks
// every call against api/openapi.yaml.
func TestActivityFilesMatchOpenAPI(t *testing.T) {
	base, url := startServer(t)
	rec, err := NewRecorder(context.Background(), "../../api/openapi.yaml", base)
	require.NoError(t, err)
	c := &client{t: t, base: base, http: &http.Client{Transport: rec, Timeout: 30 * time.Second}}

	pool, err := db.Open(context.Background(), url)
	require.NoError(t, err)
	_, key, err := tenant.New(pool).CreateTenant(context.Background(), "contract-files")
	pool.Close()
	require.NoError(t, err)
	r := c.json("POST", "/v1/users", key, `{"external_ref":"contract-files-user"}`)
	require.Equal(t, 201, r.status, "%s", r.body)
	var user struct{ ID string }
	r.json(t, &user)
	r = c.json("POST", "/v1/users/"+user.ID+"/tokens", key, `{"scopes":["upload"]}`)
	require.Equal(t, 201, r.status, "%s", r.body)
	var tok struct{ Token string }
	r.json(t, &tok)

	part := func(format string, file []byte) io.Reader {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		_, err := w.Write(activityfiles.Line(format, file))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		return &b
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(activityDir, name))
		require.NoError(t, err)
		return b
	}
	upload := func(secret, typ, batch string, body io.Reader) reply {
		return c.do("POST", "/v1/ingest/activity-files", secret, body, map[string]string{
			"Content-Type": "application/gzip",
			"X-OHS-Type":   typ, "X-OHS-Day": "2026-03-01", "X-OHS-Batch": batch, "X-OHS-Part": "0",
		})
	}

	type stored struct {
		Key       string `json:"key"`
		Duplicate bool   `json:"duplicate"`
	}
	ride := read("garmin_ride.fit")
	r = upload(tok.Token, "fit", "af1-ride", part("fit", ride))
	require.Equal(t, 200, r.status, "%s", r.body)
	var first stored
	r.json(t, &first)
	assert.False(t, first.Duplicate)
	assert.Contains(t, first.Key, "/activity_files/2026-03-01/af1-ride_0.ndjson.gz")
	r = upload(tok.Token, "fit", "af1-ride", part("fit", ride))
	require.Equal(t, 200, r.status, "%s", r.body)
	var again stored
	r.json(t, &again)
	assert.True(t, again.Duplicate, "the same file again")
	r = upload(tok.Token, "gpx", "af1-run", part("gpx", read("garmin_run.gpx")))
	require.Equal(t, 200, r.status, "%s", r.body)
	r = upload(tok.Token, "tcx", "af1-polar", part("tcx", read("polar_run.tcx")))
	require.Equal(t, 200, r.status, "%s", r.body)

	// Refusals: nothing is stored.
	r = upload(tok.Token, "fit", "af1-monitoring", part("fit", read("monitoring.fit")))
	assert.Equal(t, 400, r.status, "%s", r.body)
	assert.Contains(t, string(r.body), "holds no activity")
	assert.Equal(t, 400, upload(tok.Token, "gpx", "af1-mismatch", part("fit", ride)).status, "type differs from the header")
	assert.Equal(t, 400, upload(tok.Token, "fit", "af1-crc", part("fit", read("bad_crc.fit"))).status)
	assert.Equal(t, 400, upload(tok.Token, "fit", "af1-plain", strings.NewReader("x")).status, "not gzip")
	assert.Equal(t, 401, upload("", "fit", "af1-anon", part("fit", ride)).status)
	assert.Equal(t, 403, upload(key, "fit", "af1-key", part("fit", ride)).status, "a tenant key cannot upload")
	assert.Equal(t, 409, upload(tok.Token, "fit", "af1-ride", part("fit", read("coros_run.fit"))).status,
		"other bytes under a stored key")

	// Processed.
	deadline := time.Now().Add(60 * time.Second)
	for {
		r := c.json("GET", "/v1/users/"+user.ID+"/coverage", key, "")
		require.Equal(t, 200, r.status, "%s", r.body)
		var cov struct {
			PendingJobs int `json:"pending_jobs"`
		}
		r.json(t, &cov)
		if cov.PendingJobs == 0 {
			break
		}
		require.True(t, time.Now().Before(deadline), "jobs still pending: %s", r.body)
		time.Sleep(200 * time.Millisecond)
	}

	u := "/v1/users/" + user.ID
	r = c.json("GET", u+"/workouts?start=2026-03-01&end=2026-03-06", key, "")
	require.Equal(t, 200, r.status, "%s", r.body)
	var workouts struct {
		Data []struct {
			ID           int64  `json:"id"`
			ActivityType string `json:"activity_type"`
			Provider     string `json:"provider"`
			Source       string `json:"source"`
			Device       string `json:"device"`
		} `json:"data"`
	}
	r.json(t, &workouts)
	require.Len(t, workouts.Data, 3, "%s", r.body)
	var got []string
	var rideID int64
	for _, w := range workouts.Data {
		assert.Equal(t, "activity_files", w.Provider)
		got = append(got, w.Source+"|"+w.ActivityType+"|"+w.Device)
		if w.Device == "Garmin edge_530" {
			rideID = w.ID
		}
	}
	assert.ElementsMatch(t, []string{
		"garmin|cycling|Garmin edge_530", "garmin|running|", "polar|running|Polar Vantage V3",
	}, got)
	require.NotZero(t, rideID)

	r = c.json("GET", u+"/workouts/"+strconv.FormatInt(rideID, 10)+"/route?limit=100", key, "")
	require.Equal(t, 200, r.status, "%s", r.body)
	var route struct {
		Data []struct {
			Device string `json:"device"`
		} `json:"data"`
	}
	r.json(t, &route)
	assert.Len(t, route.Data, 9)
	for _, p := range []string{
		u + "/samples?type=heart_rate&start=2026-03-01&end=2026-03-02",
		u + "/samples?type=cycling_power&start=2026-03-01&end=2026-03-02",
		u + "/samples?type=running_power&start=2026-03-05&end=2026-03-06",
	} {
		r := c.json("GET", p, key, "")
		require.Equal(t, 200, r.status, "%s: %s", p, r.body)
		assert.Contains(t, string(r.body), `"provider":"activity_files"`, p)
	}

	// The provider's data can be erased like any other provider's.
	r = c.json("DELETE", u+"/data/activity_files", key, "")
	require.Equal(t, 202, r.status, "%s", r.body)

	calls := rec.Calls()
	require.NotEmpty(t, calls)
	for _, call := range calls {
		if len(call.Problems) > 0 {
			t.Errorf("%s %s -> %d (%s):\n  %s", call.Method, call.Path, call.Status, call.Operation,
				strings.Join(call.Problems, "\n  "))
		}
	}
}
