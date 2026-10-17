// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/soma-intelligence/open-health-sync/connectors/activityfiles"
	"github.com/soma-intelligence/open-health-sync/connectors/activityfiles/export"
	ahexport "github.com/soma-intelligence/open-health-sync/connectors/applehealth/export"
)

// filesBatchPrefix names the conversion of a file into a part. Bump it when
// the line of a file changes, so a re-run after an upgrade uses fresh
// batches instead of colliding (409) with the parts already stored.
const filesBatchPrefix = "af1-"

type filesFlags struct {
	baseURL, token, since string
	dryRun, allowHTTP     bool
	concurrency           int
}

// filesJob is one file to upload: its own batch, part 0.
type filesJob struct {
	batch string
	part  ahexport.Part
}

// filesSummary counts what an import of activity files did. It holds counts
// and format names only, never a file name or a value.
type filesSummary struct {
	walk        export.Stats
	parsed      map[string]int // files that parsed as activities, by format
	workouts    int
	routePoints int
	heartRate   int
	power       int
	repeated    int            // the same file seen again in this import
	notActivity map[string]int // by format
	unreadable  map[string]int // by format
	beforeSince int
	uploaded    int
	duplicates  int
	refused     map[string]int // by server answer
}

func (s *filesSummary) add(m *map[string]int, k string) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[k]++
}

// runImportFiles finds activity files under path and uploads each, one part
// per file, to POST /v1/ingest/activity-files. A file the server refuses is
// counted and the run goes on; a refused token or a part that keeps failing
// stops it, and running it again resumes it, since files already stored are
// answered as duplicates.
func runImportFiles(path string, f filesFlags, out io.Writer) error {
	var since time.Time
	if f.since != "" {
		t, err := time.Parse(time.DateOnly, f.since)
		if err != nil {
			return usageErr("--since takes a day as YYYY-MM-DD")
		}
		since = t
	}
	var endpoint string
	if !f.dryRun {
		var err error
		if endpoint, err = ingestEndpoint(f.baseURL, f.allowHTTP, "activity-files"); err != nil {
			return err
		}
		if strings.TrimSpace(f.token) == "" {
			return usageErr("--token (or OHS_TOKEN) is required: a user token (ohs_ut_) with the upload scope")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		sum      filesSummary
		firstErr error
		last     = time.Now()
	)
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	// progress prints the running totals every two seconds at most. The
	// caller holds mu.
	progress := func() {
		if time.Since(last) < 2*time.Second {
			return
		}
		last = time.Now()
		if f.dryRun {
			fmt.Fprintf(out, "  %d activity files read\n", sum.files())
			return
		}
		fmt.Fprintf(out, "  %d activity files sent so far (%d already stored)\n", sum.uploaded, sum.duplicates)
	}

	jobs := make(chan filesJob)
	var wg sync.WaitGroup
	if f.dryRun {
		fmt.Fprintf(out, "dry run: reading %s, nothing is uploaded\n", baseName(path))
	} else {
		base := ahexport.Uploader{Endpoint: endpoint, Token: strings.TrimSpace(f.token)}
		fmt.Fprintf(out, "importing %s into %s\n", baseName(path), redacted(endpoint))
		for range f.concurrency {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					up := base
					up.Batch = j.batch
					dup, err := up.Upload(ctx, j.part)
					mu.Lock()
					var se *ahexport.StatusError
					switch {
					case errors.As(err, &se):
						sum.add(&sum.refused, refusal(se))
					case err != nil:
						fail(err)
					default:
						sum.uploaded++
						if dup {
							sum.duplicates++
						}
						progress()
					}
					mu.Unlock()
				}
			}()
		}
	}

	// The walk is sequential; the lock is for the counts the workers share.
	seen := map[[32]byte]bool{}
	accept := func(file export.File) (activityfiles.Info, bool) {
		mu.Lock()
		defer mu.Unlock()
		if seen[file.SHA256] {
			sum.repeated++
			return activityfiles.Info{}, false
		}
		seen[file.SHA256] = true
		in, err := activityfiles.Inspect(file.Format, file.Data)
		switch {
		case errors.Is(err, activityfiles.ErrNotActivity):
			sum.add(&sum.notActivity, file.Format)
			return in, false
		case err != nil:
			sum.add(&sum.unreadable, file.Format)
			return in, false
		case !since.IsZero() && in.Start.Before(since):
			sum.beforeSince++
			return in, false
		}
		sum.add(&sum.parsed, file.Format)
		sum.workouts += in.Workouts
		sum.routePoints += in.RoutePoints
		sum.heartRate += in.HeartRate
		sum.power += in.Power
		if f.dryRun {
			progress()
		}
		return in, true
	}
	walkStats, walkErr := export.Walk(ctx, path, func(file export.File) error {
		in, ok := accept(file)
		if !ok || f.dryRun {
			return nil
		}
		j := filesJob{
			batch: filesBatchPrefix + hex.EncodeToString(file.SHA256[:])[:32],
			part: ahexport.Part{
				Type: file.Format, Day: in.Start.UTC().Format(time.DateOnly), Lines: 1,
				NDJSON: activityfiles.Line(file.Format, file.Data),
			},
		}
		select {
		case jobs <- j:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	close(jobs)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	sum.walk = walkStats
	sum.print(out, f.dryRun)

	// An upload failure cancels the walk: report the failure, not the
	// cancellation it caused.
	err := firstErr
	if err == nil {
		err = walkErr
	}
	if err != nil && sum.uploaded > 0 {
		return fmt.Errorf("%w (run the same command again to resume: files already stored are answered as duplicates)", err)
	}
	if err == nil && len(sum.refused) > 0 {
		n := 0
		for _, v := range sum.refused {
			n += v
		}
		return fmt.Errorf("the server refused %d activity file(s), listed above; the others are stored", n)
	}
	return err
}

// refusal names a server's refusal of a file by status and problem, which
// never quote the file.
func refusal(se *ahexport.StatusError) string {
	if se.Detail == "" {
		return fmt.Sprintf("HTTP %d", se.Code)
	}
	return fmt.Sprintf("HTTP %d: %s", se.Code, se.Detail)
}

func (s *filesSummary) files() int {
	n := 0
	for _, v := range s.parsed {
		n += v
	}
	return n
}

func (s *filesSummary) print(out io.Writer, dryRun bool) {
	row := func(label string, n int) { fmt.Fprintf(out, "  %-24s%d\n", label, n) }
	if dryRun {
		fmt.Fprintln(out, "dry run done:")
	} else {
		fmt.Fprintln(out, "done:")
	}
	byKey := func(title string, m map[string]int) {
		if len(m) == 0 {
			return
		}
		fmt.Fprintln(out, title)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "    %-40s %d\n", k, m[k])
		}
	}
	row("activity files", s.files())
	byKey("  by format:", s.parsed)
	row("workouts", s.workouts)
	row("route points", s.routePoints)
	row("heart rate samples", s.heartRate)
	row("power samples", s.power)
	if !dryRun {
		// Each file sent is new to the server, one a previous run already
		// stored, or refused.
		row("stored now", s.uploaded-s.duplicates)
		row("already stored", s.duplicates)
	}
	row("before --since", s.beforeSince)
	row("same file twice", s.repeated)
	row("zips opened", s.walk.Archives)
	row("other files", s.walk.Other)
	if s.walk.TooLarge > 0 {
		row("too large", s.walk.TooLarge)
	}
	if s.walk.Unreadable > 0 {
		row("unreadable .gz or zip", s.walk.Unreadable)
	}
	byKey("  skipped, not an activity:", s.notActivity)
	byKey("  skipped, could not be read:", s.unreadable)
	byKey("  refused by the server:", s.refused)
}
