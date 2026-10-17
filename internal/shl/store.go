// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/soma-intelligence/open-health-sync/internal/consent"
	"github.com/soma-intelligence/open-health-sync/internal/webhook"
)

// Share states. Expired is not stored: it is an active share past its
// expiry.
const (
	StatusActive   = "active"
	StatusExpired  = "expired"
	StatusRevoked  = "revoked"
	StatusDisabled = "disabled"
)

// Why a share stopped before its expiry.
const (
	ReasonOwner            = "owner"
	ReasonConsent          = "consent"
	ReasonProviderErased   = "provider_erased"
	ReasonPasscodeAttempts = "passcode_attempts"
)

// Access outcomes. File is an audit outcome only: the download of a
// location's file.
const (
	OutcomeServed        = "served"
	OutcomeWrongPasscode = "wrong_passcode"
	OutcomeInactive      = "inactive"
	OutcomeFile          = "file"
)

// Store limits.
const (
	// DefaultLimit and MaxLimit bound a page of the share list.
	DefaultLimit = 100
	MaxLimit     = 1000
	// RecentAccesses is how many accesses a share's detail lists.
	RecentAccesses = 50
	// TicketTTL is how long the location of a file works, once.
	TicketTTL = 15 * time.Minute
	// MaxRecipient bounds the name a reader gives, in characters.
	MaxRecipient = 256
)

// Errors of the store.
var (
	ErrNotFound      = errors.New("shl: share not found")
	ErrUserNotFound  = errors.New("shl: user not found")
	ErrInvalidCursor = errors.New("shl: the cursor is not valid")
	// ErrBusy: the share or its user stayed locked past LockTimeout.
	ErrBusy = errors.New("shl: the share is busy")
)

// Querier runs reads (a *pgxpool.Pool or a pgx.Tx).
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Execer runs reads and writes inside the caller's transaction.
type Execer interface {
	Querier
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DB runs writes, each in a transaction of its own (a *pgxpool.Pool).
type DB interface {
	Execer
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Share is a share as its owner reads it: never a key, a link, a manifest
// id or a passcode.
type Share struct {
	ID               uuid.UUID  `json:"id"`
	Label            string     `json:"label"`
	Categories       []string   `json:"categories"`
	Start            time.Time  `json:"start"`
	End              time.Time  `json:"end"`
	ExpiresAt        time.Time  `json:"expires_at"`
	Passcode         bool       `json:"passcode"`
	PasscodeFailures int        `json:"passcode_failures"`
	Status           string     `json:"status"`
	RevokedAt        *time.Time `json:"revoked_at"`
	RevokedReason    *string    `json:"revoked_reason"`
	Observations     int        `json:"observations"`
	FileBytes        int64      `json:"file_bytes"`
	Accesses         int        `json:"accesses"`
	LastAccessedAt   *time.Time `json:"last_accessed_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Access is one manifest request on a share.
type Access struct {
	At        time.Time `json:"at"`
	Recipient string    `json:"recipient"`
	Outcome   string    `json:"outcome"`
}

// Detail is a share with its latest accesses, newest first.
type Detail struct {
	Share
	RecentAccesses []Access `json:"recent_accesses"`
}

// shareCols reads a Share from ohs_sync.shares s. Accesses counts the
// served manifests.
const shareCols = `s.id, s.label, s.categories, s.data_start, s.data_end, s.expires_at, s.passcode_hash IS NOT NULL,
	s.passcode_failures,
	CASE WHEN s.status = 'active' AND s.expires_at <= now() THEN 'expired' ELSE s.status END,
	s.revoked_at, s.revoked_reason, s.observations, s.file_bytes,
	(SELECT count(*) FROM ohs_sync.share_accesses a
	 WHERE a.tenant_id = s.tenant_id AND a.user_id = s.user_id AND a.share_id = s.id AND a.outcome = 'served'),
	(SELECT max(a.at) FROM ohs_sync.share_accesses a
	 WHERE a.tenant_id = s.tenant_id AND a.user_id = s.user_id AND a.share_id = s.id AND a.outcome = 'served'),
	s.created_at`

func scanShare(row pgx.Row) (Share, error) {
	var sh Share
	err := row.Scan(&sh.ID, &sh.Label, &sh.Categories, &sh.Start, &sh.End, &sh.ExpiresAt, &sh.Passcode,
		&sh.PasscodeFailures, &sh.Status, &sh.RevokedAt, &sh.RevokedReason, &sh.Observations, &sh.FileBytes,
		&sh.Accesses, &sh.LastAccessedAt, &sh.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Share{}, ErrNotFound
	}
	if err != nil {
		return Share{}, err
	}
	sh.Start, sh.End, sh.ExpiresAt, sh.CreatedAt = sh.Start.UTC(), sh.End.UTC(), sh.ExpiresAt.UTC(), sh.CreatedAt.UTC()
	for _, p := range []**time.Time{&sh.RevokedAt, &sh.LastAccessedAt} {
		if *p != nil {
			t := (*p).UTC()
			*p = &t
		}
	}
	return sh, nil
}

// userExists reports whether user is an active user of tenant.
func userExists(ctx context.Context, db Querier, tenant, user uuid.UUID) error {
	var ok bool
	if err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM ohs_core.end_users WHERE tenant_id = $1 AND id = $2 AND status = 'active')`,
		tenant, user).Scan(&ok); err != nil {
		return fmt.Errorf("shl: user: %w", err)
	}
	if !ok {
		return ErrUserNotFound
	}
	return nil
}

// lockUser holds the user row FOR SHARE until tx ends, like every writer of
// a user's rows, and returns its status: a deletion in flight finishes
// first, and one that starts later waits for this write.
func lockUser(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM ohs_core.end_users WHERE tenant_id = $1 AND id = $2 FOR SHARE`,
		tenant, user).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("shl: lock user: %w", err)
	}
	return status, nil
}

// QuantityTypes lists the quantity types the user has samples of in
// [start, end).
func QuantityTypes(ctx context.Context, db Querier, tenant, user uuid.UUID, start, end time.Time) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT quantity_type FROM ohs_data.quantity_samples
		WHERE tenant_id = $1 AND user_id = $2 AND start_date >= $3 AND start_date < $4`, tenant, user, start, end)
	if err != nil {
		return nil, fmt.Errorf("shl: quantity types: %w", err)
	}
	types, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("shl: quantity types: %w", err)
	}
	return types, nil
}

// Refused returns the sources (Source(provider, category)) a share must not
// hold: a category the user's consent refuses, or one whose purge is still
// pending.
func Refused(ctx context.Context, db Querier, tenant, user uuid.UUID) (map[string]bool, error) {
	rows, err := db.Query(ctx, `
		SELECT provider, category FROM ohs_sync.consents
		WHERE tenant_id = $1 AND user_id = $2 AND NOT granted
		UNION
		SELECT provider, category FROM ohs_sync.consent_purges
		WHERE tenant_id = $1 AND user_id = $2 AND finished_at IS NULL`, tenant, user)
	if err != nil {
		return nil, fmt.Errorf("shl: refused sources: %w", err)
	}
	pairs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var p, c string
		err := r.Scan(&p, &c)
		return Source(p, c), err
	})
	if err != nil {
		return nil, fmt.Errorf("shl: refused sources: %w", err)
	}
	out := make(map[string]bool, len(pairs))
	for _, p := range pairs {
		out[p] = true
	}
	return out, nil
}

// Erasing returns the providers whose data of the user is queued for
// erasure: a share leaves their records out, since the erasure would
// revoke it at once.
func Erasing(ctx context.Context, db Querier, tenant, user uuid.UUID) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT provider FROM ohs_core.deletion_jobs
		WHERE tenant_id = $1 AND user_id = $2 AND provider IS NOT NULL AND status IN ('pending', 'running')`,
		tenant, user)
	if err != nil {
		return nil, fmt.Errorf("shl: erasing providers: %w", err)
	}
	provs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("shl: erasing providers: %w", err)
	}
	out := make(map[string]bool, len(provs))
	for _, p := range provs {
		out[p] = true
	}
	return out, nil
}

// Begin starts the transaction a share is made in. It holds the user row
// FOR SHARE and the user's consent lock until it ends, and the share's
// records are read inside it: an erasure (which locks the user row FOR
// UPDATE) or a consent change that starts meanwhile waits for the share,
// then finds it and revokes it, and one that committed before is already
// in what the share reads. A user who is not active is ErrUserNotFound.
func Begin(ctx context.Context, db DB, tenant, user uuid.UUID) (pgx.Tx, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("shl: begin: %w", err)
	}
	status, err := lockUser(ctx, tx, tenant, user)
	if err == nil && status != "active" {
		err = ErrUserNotFound
	}
	if err == nil {
		err = consent.Lock(ctx, tx, tenant, user)
	}
	if err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return tx, nil
}

// NewShare is what Create stores.
type NewShare struct {
	Label        string
	Categories   []string
	Start, End   time.Time
	ExpiresAt    time.Time
	PasscodeHash string // "" for none
	ManifestHash string
	JWE          string
	Observations int
	Sources      []string
}

// Create stores a share and queues share.created in tx, the transaction
// Begin started and the share's records were read in, and commits it.
func Create(ctx context.Context, tx pgx.Tx, tenant, user uuid.UUID, n NewShare) (Share, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Share{}, err
	}
	var passcode *string
	if n.PasscodeHash != "" {
		passcode = &n.PasscodeHash
	}
	if n.Sources == nil {
		n.Sources = []string{}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO ohs_sync.shares
		(id, tenant_id, user_id, manifest_hash, label, categories, data_start, data_end, expires_at, passcode_hash,
		 jwe, file_bytes, observations, sources)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		id, tenant, user, n.ManifestHash, n.Label, n.Categories, n.Start, n.End, n.ExpiresAt, passcode,
		n.JWE, len(n.JWE), n.Observations, n.Sources); err != nil {
		return Share{}, fmt.Errorf("shl: create: %w", err)
	}
	if _, err := webhook.Enqueue(ctx, tx, webhook.Event{Tenant: tenant, User: &user, Type: webhook.ShareCreated,
		Data: map[string]any{"share_id": id, "categories": n.Categories, "expires_at": n.ExpiresAt.UTC(),
			"passcode": passcode != nil}}); err != nil {
		return Share{}, err
	}
	sh, err := scanShare(tx.QueryRow(ctx, `SELECT `+shareCols+` FROM ohs_sync.shares s
		WHERE s.tenant_id = $1 AND s.user_id = $2 AND s.id = $3`, tenant, user, id))
	if err != nil {
		return Share{}, fmt.Errorf("shl: create: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Share{}, fmt.Errorf("shl: commit: %w", err)
	}
	return sh, nil
}

// Query pages the share list.
type Query struct {
	Cursor string
	// Limit is the page size: 0 means DefaultLimit, at most MaxLimit.
	Limit int
}

// Page is one page of shares; NextCursor is empty on the last page.
type Page struct {
	Records    []Share
	NextCursor string
}

func encodeCursor(t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	t, err1 := time.Parse(time.RFC3339Nano, ts)
	id, err2 := uuid.Parse(idStr)
	if err1 != nil || err2 != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	return t, id, nil
}

// List returns the user's shares, oldest first.
func List(ctx context.Context, db Querier, tenant, user uuid.UUID, q Query) (Page, error) {
	limit := q.Limit
	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 0 || limit > MaxLimit:
		return Page{}, fmt.Errorf("shl: limit must be between 1 and %d", MaxLimit)
	}
	if err := userExists(ctx, db, tenant, user); err != nil {
		return Page{}, err
	}
	args := []any{tenant, user}
	where := "s.tenant_id = $1 AND s.user_id = $2"
	if q.Cursor != "" {
		t, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return Page{}, err
		}
		args = append(args, t, id)
		where += " AND (s.created_at, s.id) > ($3, $4)"
	}
	args = append(args, limit+1)
	rows, err := db.Query(ctx, `SELECT `+shareCols+` FROM ohs_sync.shares s WHERE `+where+
		` ORDER BY s.created_at, s.id LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return Page{}, fmt.Errorf("shl: list: %w", err)
	}
	defer rows.Close()
	out := Page{Records: []Share{}}
	for rows.Next() {
		if len(out.Records) == limit {
			last := out.Records[len(out.Records)-1]
			out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
			break
		}
		sh, err := scanShare(rows)
		if err != nil {
			return Page{}, fmt.Errorf("shl: list: %w", err)
		}
		out.Records = append(out.Records, sh)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("shl: list: %w", err)
	}
	return out, nil
}

// Get returns one share of the user with its latest accesses.
func Get(ctx context.Context, db Querier, tenant, user, id uuid.UUID) (Detail, error) {
	if err := userExists(ctx, db, tenant, user); err != nil {
		return Detail{}, err
	}
	sh, err := scanShare(db.QueryRow(ctx, `SELECT `+shareCols+` FROM ohs_sync.shares s
		WHERE s.tenant_id = $1 AND s.user_id = $2 AND s.id = $3`, tenant, user, id))
	if errors.Is(err, ErrNotFound) {
		return Detail{}, err
	}
	if err != nil {
		return Detail{}, fmt.Errorf("shl: get: %w", err)
	}
	rows, err := db.Query(ctx, `SELECT at, recipient, outcome FROM ohs_sync.share_accesses
		WHERE tenant_id = $1 AND user_id = $2 AND share_id = $3 ORDER BY id DESC LIMIT $4`,
		tenant, user, id, RecentAccesses)
	if err != nil {
		return Detail{}, fmt.Errorf("shl: accesses: %w", err)
	}
	acc, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Access, error) {
		var a Access
		err := r.Scan(&a.At, &a.Recipient, &a.Outcome)
		a.At = a.At.UTC()
		return a, err
	})
	if err != nil {
		return Detail{}, fmt.Errorf("shl: accesses: %w", err)
	}
	if acc == nil {
		acc = []Access{}
	}
	return Detail{Share: sh, RecentAccesses: acc}, nil
}

// revokeSQL stops the matching active shares of one user and removes their
// file; $3 is the reason, the condition follows. A share already past its
// expiry stays expired (its file, which the sweep would remove, goes now)
// and is not returned as revoked.
const revokeSQL = `UPDATE ohs_sync.shares SET
		status = CASE WHEN expires_at > now() THEN 'revoked' ELSE status END,
		revoked_reason = CASE WHEN expires_at > now() THEN $3 ELSE revoked_reason END,
		revoked_at = CASE WHEN expires_at > now() THEN now() ELSE revoked_at END,
		jwe = NULL
	WHERE tenant_id = $1 AND user_id = $2 AND status = 'active' AND `

// revoke runs revokeSQL with cond and queues share.revoked for each share
// it stopped before its expiry.
func revoke(ctx context.Context, tx Execer, tenant, user uuid.UUID, reason, cond string, args ...any) (int, error) {
	rows, err := tx.Query(ctx, revokeSQL+cond+` RETURNING id, status = 'revoked'`, append([]any{tenant, user, reason}, args...)...)
	if err != nil {
		return 0, fmt.Errorf("shl: revoke: %w", err)
	}
	type stopped struct {
		id      uuid.UUID
		revoked bool
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (stopped, error) {
		var s stopped
		err := r.Scan(&s.id, &s.revoked)
		return s, err
	})
	if err != nil {
		return 0, fmt.Errorf("shl: revoke: %w", err)
	}
	n := 0
	for _, s := range all {
		if !s.revoked {
			continue
		}
		if err := revokedEvent(ctx, tx, tenant, user, s.id, reason); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

func revokedEvent(ctx context.Context, tx Execer, tenant, user, id uuid.UUID, reason string) error {
	_, err := webhook.Enqueue(ctx, tx, webhook.Event{Tenant: tenant, User: &user, Type: webhook.ShareRevoked,
		Data: map[string]any{"share_id": id, "reason": reason}})
	return err
}

// Revoke stops one share of the user at its owner's request. Revoking a
// share that is no longer active changes nothing; one that does not exist
// is ErrNotFound.
func Revoke(ctx context.Context, db DB, tenant, user, id uuid.UUID) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("shl: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	status, err := lockUser(ctx, tx, tenant, user)
	if err != nil {
		return err
	}
	if status != "active" {
		return ErrUserNotFound
	}
	n, err := revoke(ctx, tx, tenant, user, ReasonOwner, "id = $4", id)
	if err != nil {
		return err
	}
	if n == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ohs_sync.shares
			WHERE tenant_id = $1 AND user_id = $2 AND id = $3)`, tenant, user, id).Scan(&exists); err != nil {
			return fmt.Errorf("shl: revoke: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("shl: commit: %w", err)
	}
	return nil
}

// RevokeSources stops, in the caller's transaction, every active share of
// the user that holds records of provider in one of categories: a consent
// change refused them.
func RevokeSources(ctx context.Context, tx Execer, tenant, user uuid.UUID, provider string, categories []string) (int, error) {
	if len(categories) == 0 {
		return 0, nil
	}
	srcs := make([]string, len(categories))
	for i, c := range categories {
		srcs[i] = Source(provider, c)
	}
	return revoke(ctx, tx, tenant, user, ReasonConsent, "sources && $4::text[]", srcs)
}

// RevokeProvider stops, in the caller's transaction, every active share of
// the user that holds records of provider: they were erased.
func RevokeProvider(ctx context.Context, tx Execer, tenant, user uuid.UUID, provider string) (int, error) {
	return revoke(ctx, tx, tenant, user, ReasonProviderErased,
		"EXISTS (SELECT 1 FROM unnest(sources) AS src WHERE split_part(src, '/', 1) = $4)", provider)
}

// PurgeExpired removes the file of every share past its expiry, user by
// user, and returns how many it removed.
func PurgeExpired(ctx context.Context, db DB) (int64, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT tenant_id, user_id FROM ohs_sync.shares
		WHERE jwe IS NOT NULL AND expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("shl: expired shares: %w", err)
	}
	users, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]uuid.UUID, error) {
		var u [2]uuid.UUID
		err := r.Scan(&u[0], &u[1])
		return u, err
	})
	if err != nil {
		return 0, fmt.Errorf("shl: expired shares: %w", err)
	}
	var total int64
	for _, u := range users {
		tag, err := db.Exec(ctx, `UPDATE ohs_sync.shares SET jwe = NULL
			WHERE tenant_id = $1 AND user_id = $2 AND jwe IS NOT NULL AND expires_at <= now()`, u[0], u[1])
		if err != nil {
			return total, fmt.Errorf("shl: purge expired: %w", err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}
