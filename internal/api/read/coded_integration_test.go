// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package read

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/internal/db"
	"github.com/soma-intelligence/open-health-sync/internal/tenant"
)

// recorder keeps the statements an Observations call sends.
type recorder struct {
	Querier
	stmts []recorded
}

type recorded struct {
	sql  string
	args []any
}

func (r *recorder) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	r.stmts = append(r.stmts, recorded{sql, args})
	return r.Querier.Query(ctx, sql, args...)
}

var chunkName = regexp.MustCompile(`_hyper_\d+_\d+_chunk`)

// planChunks is the set of chunks the plan of st reads.
func planChunks(t *testing.T, pool *pgxpool.Pool, st recorded) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), "EXPLAIN (COSTS OFF) "+st.sql, st.args...)
	require.NoError(t, err)
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	out := map[string]bool{}
	for _, c := range chunkName.FindAllString(strings.Join(lines, "\n"), -1) {
		out[c] = true
	}
	return out
}

// A page of Observations reads its records back by id. Those reads, the
// blood pressure pairing subqueries included, are bounded by the page's
// times, so the planner keeps only the chunks the page lies in instead of
// every chunk of the user's history (a year of data is over 50 weekly chunks,
// and planning them all cost more than the read itself).
func TestObservationsReadOnlyThePagesChunks(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, db.TestDB(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store := tenant.New(pool)
	tn, _, err := store.CreateTenant(ctx, "chunks")
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, tn.ID, "u")
	require.NoError(t, err)

	// Twelve weeks, one blood pressure pair and one heart rate a day.
	t0 := time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC)
	insert := func(typ, unit string, v float64, at time.Time) {
		_, err := pool.Exec(ctx, `INSERT INTO ohs_data.quantity_samples
			(tenant_id, user_id, provider, source_slug, device_id, quantity_type, value, unit, start_date, content_hash)
			VALUES ($1, $2, 'withings', 'withings', 'cuff-1', $3, $4, $5, $6, $7)`,
			tn.ID, u.ID, typ, v, unit, at, uuid.NewString())
		require.NoError(t, err)
	}
	for d := 0; d < 84; d++ {
		at := t0.AddDate(0, 0, d)
		insert(Systolic, "mmHg", 120, at)
		insert(Diastolic, "mmHg", 80, at)
		insert("heart_rate", "bpm", 60, at.Add(time.Hour))
	}
	var all int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM show_chunks('ohs_data.quantity_samples')`).Scan(&all))
	require.GreaterOrEqual(t, all, 12)

	// byID is the read back by id of the page's quantity samples.
	byID := func(r *recorder) recorded {
		for _, st := range r.stmts {
			if strings.Contains(st.sql, "q.id = ANY(") {
				return st
			}
		}
		t.Fatal("no read by id")
		return recorded{}
	}

	// One week: the listing and the read by id stay in that week's chunks.
	// (The merged view's cover lookups are not bounded by the page yet; only
	// its read by id is checked below.)
	r := &recorder{Querier: pool}
	page, err := Observations(ctx, r, tn.ID, u.ID, Query{
		Start: t0.AddDate(0, 0, 28), End: t0.AddDate(0, 0, 35), Limit: 100, View: ViewAll,
	}, AllObservations())
	require.NoError(t, err)
	require.Len(t, page.Records, 14) // 7 panels, 7 heart rates
	for _, rec := range page.Records {
		if rec.Sample.Type == Systolic {
			require.NotNil(t, rec.Partner, "a panel lost its diastolic half")
			assert.Equal(t, 80.0, rec.Partner.Value)
		}
	}
	for _, st := range r.stmts {
		if n := len(planChunks(t, pool, st)); n > 0 {
			assert.LessOrEqual(t, n, 2, "a one week page plans %d of %d chunks: %s", n, all, st.sql)
		}
	}

	// Paging the whole history: every page's read by id stays in the chunks
	// of that page, and the pages still hold every panel and sample once.
	seen := map[int64]bool{}
	cursor := ""
	for {
		r := &recorder{Querier: pool}
		page, err := Observations(ctx, r, tn.ID, u.ID, Query{Cursor: cursor, Limit: 10}, AllObservations())
		require.NoError(t, err)
		n := len(planChunks(t, pool, byID(r)))
		assert.LessOrEqual(t, n, 2, "a page of 10 plans %d of %d chunks", n, all)
		for _, rec := range page.Records {
			if rec.Kind != KindQuantity {
				continue
			}
			assert.False(t, seen[rec.Sample.ID])
			seen[rec.Sample.ID] = true
			if rec.Sample.Type == Systolic {
				require.NotNil(t, rec.Partner)
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	assert.Len(t, seen, 2*84) // 84 panels and 84 heart rates

	// A read by id alone has no time to go by and still finds the panel.
	var panel int64
	for id := range seen {
		panel = id
		break
	}
	_, err = ObservationByID(ctx, pool, tn.ID, u.ID, KindQuantity, true, panel, ViewAll)
	_, single := ObservationByID(ctx, pool, tn.ID, u.ID, KindQuantity, false, panel, ViewAll)
	assert.True(t, (err == nil) != (single == nil), "a sample is either a panel or a single sample")
}

// The merged view's lookups of a better ranked record carry the page's
// window, so they plan only the chunks near the page, and a page with no
// end is read through windows: the pages still hold every record the merged
// view keeps, once.
func TestMergedReadsAreWindowed(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, db.TestDB(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	store := tenant.New(pool)
	tn, _, err := store.CreateTenant(ctx, "windows")
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, tn.ID, "u")
	require.NoError(t, err)

	// Twelve weeks: a blood pressure pair from a cuff, and the same heart
	// rate from a watch (ranked first by default) and from the cuff.
	t0 := time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC)
	insert := func(provider, typ, unit string, v float64, at time.Time) {
		_, err := pool.Exec(ctx, `INSERT INTO ohs_data.quantity_samples
			(tenant_id, user_id, provider, source_slug, device_id, quantity_type, value, unit, start_date, content_hash)
			VALUES ($1, $2, $3, $3, 'dev-1', $4, $5, $6, $7, $8)`,
			tn.ID, u.ID, provider, typ, v, unit, at, uuid.NewString())
		require.NoError(t, err)
	}
	for d := 0; d < 84; d++ {
		at := t0.AddDate(0, 0, d)
		insert("withings", Systolic, "mmHg", 120, at)
		insert("withings", Diastolic, "mmHg", 80, at)
		insert("apple_health", "heart_rate", "bpm", 60, at.Add(time.Hour))
		insert("withings", "heart_rate", "bpm", 61, at.Add(time.Hour+time.Minute))
	}
	var all int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM show_chunks('ohs_data.quantity_samples')`).Scan(&all))
	require.GreaterOrEqual(t, all, 12)

	// coverChunks is the most chunks a merged lookup of the statements plans.
	coverChunks := func(r *recorder) int {
		most := 0
		for _, st := range r.stmts {
			if strings.Contains(st.sql, "_cover") {
				most = max(most, len(planChunks(t, pool, st)))
			}
		}
		return most
	}

	// One week: the lookups stay within the week and a day and a half on
	// each side.
	r := &recorder{Querier: pool}
	page, err := Observations(ctx, r, tn.ID, u.ID, Query{Start: t0.AddDate(0, 0, 28), End: t0.AddDate(0, 0, 35), Limit: 100}, AllObservations())
	require.NoError(t, err)
	require.Len(t, page.Records, 14) // 7 panels, 7 heart rates of the watch
	for _, rec := range page.Records {
		if rec.Sample.Type == "heart_rate" {
			assert.Equal(t, "apple_health", rec.Sample.Provider)
		}
	}
	assert.LessOrEqual(t, coverChunks(r), 3, "of %d chunks", all)

	// No end: paged through windows, every page's lookups plan a few chunks,
	// and the pages hold each kept record once.
	seen := map[int64]bool{}
	cursor := ""
	for {
		r := &recorder{Querier: pool}
		page, err := Observations(ctx, r, tn.ID, u.ID, Query{Cursor: cursor, Limit: 10}, AllObservations())
		require.NoError(t, err)
		assert.LessOrEqual(t, coverChunks(r), 4, "of %d chunks", all)
		for _, rec := range page.Records {
			assert.False(t, seen[rec.Sample.ID])
			seen[rec.Sample.ID] = true
			if rec.Sample.Type == "heart_rate" {
				assert.Equal(t, "apple_health", rec.Sample.Provider, "the cuff's heart rate is covered")
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	assert.Len(t, seen, 2*84)

	// The REST list too.
	n := 0
	cursor = ""
	for {
		r := &recorder{Querier: pool}
		page, err := Samples(ctx, r, tn.ID, u.ID, Query{Type: "heart_rate", Cursor: cursor, Limit: 10})
		require.NoError(t, err)
		assert.LessOrEqual(t, coverChunks(r), 4, "of %d chunks", all)
		for _, s := range page.Records {
			assert.Equal(t, "apple_health", s.Provider)
			n++
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	assert.Equal(t, 84, n)
}
