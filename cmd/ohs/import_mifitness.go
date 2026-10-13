// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"

	ahexport "github.com/soma-intelligence/open-health-sync/connectors/applehealth/export"
	"github.com/soma-intelligence/open-health-sync/connectors/xiaomi/export"
)

type miFitnessFlags struct {
	baseURL, token, since, batch string
	dryRun, allowHTTP            bool
	concurrency                  int
}

// runImportMiFitness converts a Mi Fitness copy and uploads its parts to
// POST /v1/ingest/xiaomi. Parts go out in order over a few connections; the
// first failure stops the run, and running it again resumes it, since the
// parts already stored are answered as duplicates.
func runImportMiFitness(path string, f miFitnessFlags, out io.Writer) error {
	opts := export.Options{Since: f.since}
	if err := opts.Validate(); err != nil {
		return usageErr(err.Error())
	}
	var endpoint string
	if !f.dryRun {
		var err error
		if endpoint, err = ingestEndpoint(f.baseURL, f.allowHTTP, "xiaomi"); err != nil {
			return err
		}
		if strings.TrimSpace(f.token) == "" {
			return usageErr("--token (or OHS_TOKEN) is required: a user token (ohs_ut_) with the upload scope")
		}
	}
	e, err := export.Open(path)
	if err != nil {
		return err
	}
	defer e.Close()
	batch := f.batch
	if batch == "" {
		batch = e.BatchID(opts)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu                   sync.Mutex
		uploaded, duplicates int
		firstErr             error
	)
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	parts := make(chan export.Part)
	var wg sync.WaitGroup
	if f.dryRun {
		fmt.Fprintf(out, "dry run: converting %s, nothing is uploaded\n", baseName(path))
	} else {
		up := &ahexport.Uploader{Endpoint: endpoint, Token: strings.TrimSpace(f.token), Batch: batch}
		fmt.Fprintf(out, "importing %s into %s, batch %s\n", baseName(path), redacted(endpoint), batch)
		for range f.concurrency {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range parts {
					dup, err := up.Upload(ctx, ahexport.Part{Number: p.Number, Type: p.Type, Day: p.Day, Lines: p.Lines, NDJSON: p.NDJSON})
					if err != nil {
						fail(err)
						continue
					}
					mu.Lock()
					uploaded++
					if dup {
						duplicates++
					}
					mu.Unlock()
				}
			}()
		}
	}
	st, convErr := e.Convert(opts, func(p export.Part) error {
		if f.dryRun {
			return nil
		}
		select {
		case parts <- p:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	close(parts)
	wg.Wait()

	row := func(label string, n int) { fmt.Fprintf(out, "  %-18s%d\n", label, n) }
	if f.dryRun {
		fmt.Fprintln(out, "dry run done:")
	} else {
		fmt.Fprintf(out, "done, batch %s:\n", batch)
	}
	row("rows read", st.Rows)
	row("lines", st.Lines)
	row("parts", st.Parts)
	if !f.dryRun {
		row("uploaded", uploaded)
		row("already stored", duplicates)
	}
	row("before --since", st.BeforeSince)
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
	byKey("  lines by table and key:", st.Keys)
	byKey("  skipped, could not be read:", st.Invalid)

	// An upload failure cancels the conversion: report the failure, not
	// the cancellation it caused.
	err = firstErr
	if err == nil {
		err = convErr
	}
	if err != nil && uploaded > 0 {
		return fmt.Errorf("%w (run the same command again to resume: parts already stored are answered as duplicates)", err)
	}
	return err
}
