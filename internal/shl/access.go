// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/soma-intelligence/open-health-sync/internal/audit"
	"github.com/soma-intelligence/open-health-sync/internal/webhook"
)

// Request is what a reader sends with a manifest request.
type Request struct {
	// Recipient is the name the reader gave (checked by the caller).
	Recipient string
	// Passcode is the passcode sent, nil when none was.
	Passcode *string
	// EmbeddedLengthMax caps the length of an embedded file; nil is no cap.
	EmbeddedLengthMax *int
}

// AuditInfo is what the audit event of an access says about the request.
type AuditInfo struct {
	Method, Route, RequestID string
}

// Result is the answer to a manifest request or a file download.
type Result struct {
	// Known is false when the secret names no share (nothing is recorded:
	// it names no tenant).
	Known   bool
	ShareID uuid.UUID
	// Outcome is OutcomeServed, OutcomeWrongPasscode or OutcomeInactive
	// (OutcomeFile for a download served).
	Outcome string
	// Remaining is the passcode attempts left after a wrong one.
	Remaining int
	// JWE is the file, embedded or downloaded; Ticket the secret of its
	// location when it is not embedded.
	JWE    string
	Ticket string
	// LastUpdated is when the file was made.
	LastUpdated time.Time
}

// LockTimeout bounds how long a public request waits for the share or its
// user: a burst on one link, or a user being erased, answers ErrBusy rather
// than holding a database connection.
const LockTimeout = 2 * time.Second

// begin starts the transaction of a public request, with LockTimeout.
func begin(ctx context.Context, db DB) (pgx.Tx, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("shl: begin: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`,
		strconv.FormatInt(LockTimeout.Milliseconds(), 10)+"ms"); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("shl: lock timeout: %w", err)
	}
	return tx, nil
}

// busy maps a lock wait past LockTimeout to ErrBusy.
func busy(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "55P03" { // lock_not_available
		return ErrBusy
	}
	return err
}

// OpenManifest answers a manifest request for the share whose manifest id
// is manifestID. The passcode is checked first, holding no lock and no
// connection: its hash never changes, and PBKDF2 takes tens of
// milliseconds no other request should wait on. Then one transaction holds
// the user row and the share row: wrong passcodes are counted one at a
// time, and the access row, its audit event and its webhook commit with
// the answer. A share that is revoked, disabled, expired, without a file,
// or whose user is not active is OutcomeInactive. A wrong or missing
// passcode counts; the MaxPasscodeFailures-th disables the share and
// removes its file. A lock held past LockTimeout is ErrBusy.
func OpenManifest(ctx context.Context, db DB, manifestID string, req Request, info AuditInfo) (Result, error) {
	hash := HashSecret(manifestID)
	var (
		tenant, user uuid.UUID
		passHash     *string
		live         bool
	)
	// The one lookup without tenant and user: the secret's hash is all the
	// request has. Every query after it is scoped to the row's tenant and user.
	err := db.QueryRow(ctx, `SELECT tenant_id, user_id, passcode_hash,
			status = 'active' AND expires_at > now() AND jwe IS NOT NULL
		FROM ohs_sync.shares WHERE manifest_hash = $1`, hash).Scan(&tenant, &user, &passHash, &live)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("shl: manifest: %w", err)
	}
	// A share that no longer works never works again: its passcode is not
	// worth checking.
	passOK := passHash == nil || (live && req.Passcode != nil && VerifyPasscode(*passHash, *req.Passcode))

	tx, err := begin(ctx, db)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	userStatus, err := lockUser(ctx, tx, tenant, user)
	if errors.Is(err, ErrUserNotFound) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, busy(err)
	}
	var (
		id       uuid.UUID
		status   string
		expires  time.Time
		failures int
		size     *int
		created  time.Time
		now      time.Time
	)
	err = tx.QueryRow(ctx, `SELECT id, status, expires_at, passcode_failures, octet_length(jwe), created_at, now()
		FROM ohs_sync.shares WHERE tenant_id = $1 AND user_id = $2 AND manifest_hash = $3 FOR UPDATE`,
		tenant, user, hash).Scan(&id, &status, &expires, &failures, &size, &created, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, busy(fmt.Errorf("shl: manifest: %w", err))
	}
	res := Result{Known: true, ShareID: id, LastUpdated: created.UTC()}
	a := access{tx: tx, tenant: tenant, user: user, share: id, info: info}

	if userStatus != "active" || status != StatusActive || !now.Before(expires) || size == nil {
		res.Outcome = OutcomeInactive
		return res, a.finish(ctx, req.Recipient, OutcomeInactive, http.StatusNotFound, nil, nil)
	}
	if !passOK {
		failures++
		disable := failures >= MaxPasscodeFailures
		if _, err := tx.Exec(ctx, `UPDATE ohs_sync.shares SET passcode_failures = $4,
				status = CASE WHEN $5 THEN 'disabled' ELSE status END,
				revoked_reason = CASE WHEN $5 THEN 'passcode_attempts' ELSE revoked_reason END,
				revoked_at = CASE WHEN $5 THEN now() ELSE revoked_at END,
				jwe = CASE WHEN $5 THEN NULL ELSE jwe END
			WHERE tenant_id = $1 AND user_id = $2 AND id = $3`, tenant, user, id, failures, disable); err != nil {
			return Result{}, fmt.Errorf("shl: count passcode: %w", err)
		}
		if disable {
			if err := revokedEvent(ctx, tx, tenant, user, id, ReasonPasscodeAttempts); err != nil {
				return Result{}, err
			}
		}
		res.Outcome, res.Remaining = OutcomeWrongPasscode, RemainingAttempts(failures)
		return res, a.finish(ctx, req.Recipient, OutcomeWrongPasscode, http.StatusUnauthorized, nil, nil)
	}

	res.Outcome = OutcomeServed
	var ticketHash *string
	var ticketExp *time.Time
	if req.EmbeddedLengthMax != nil && *size > *req.EmbeddedLengthMax {
		t, err := NewSecret()
		if err != nil {
			return Result{}, err
		}
		h, exp := HashSecret(t), now.Add(TicketTTL)
		ticketHash, ticketExp, res.Ticket = &h, &exp, t
	} else if err := tx.QueryRow(ctx, `SELECT jwe FROM ohs_sync.shares WHERE tenant_id = $1 AND user_id = $2 AND id = $3`,
		tenant, user, id).Scan(&res.JWE); err != nil {
		return Result{}, fmt.Errorf("shl: manifest: %w", err)
	}
	if _, err := webhook.Enqueue(ctx, tx, webhook.Event{Tenant: tenant, User: &user, Type: webhook.ShareAccessed,
		Data: map[string]any{"share_id": id}}); err != nil {
		return Result{}, err
	}
	return res, a.finish(ctx, req.Recipient, OutcomeServed, http.StatusOK, ticketHash, ticketExp)
}

// TakeFile serves the file of a location once: the ticket must be unused,
// not past its TicketTTL, and its share still active. A ticket that names
// nothing is not recorded; any other download is, as OutcomeFile or
// OutcomeInactive.
func TakeFile(ctx context.Context, db DB, ticket string, info AuditInfo) (Result, error) {
	hash := HashSecret(ticket)
	var tenant, user uuid.UUID
	// As in OpenManifest, the one lookup by secret alone.
	err := db.QueryRow(ctx, `SELECT tenant_id, user_id FROM ohs_sync.share_accesses WHERE ticket_hash = $1`, hash).
		Scan(&tenant, &user)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("shl: file: %w", err)
	}
	tx, err := begin(ctx, db)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	userStatus, err := lockUser(ctx, tx, tenant, user)
	if errors.Is(err, ErrUserNotFound) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, busy(err)
	}
	var (
		accessID   int64
		shareID    uuid.UUID
		ticketExp  *time.Time
		ticketUsed *time.Time
		status     string
		expires    time.Time
		jwe        *string
		created    time.Time
		now        time.Time
	)
	err = tx.QueryRow(ctx, `SELECT a.id, a.share_id, a.ticket_expires_at, a.ticket_used_at, s.status, s.expires_at,
			s.jwe, s.created_at, now()
		FROM ohs_sync.share_accesses a
		JOIN ohs_sync.shares s ON s.tenant_id = a.tenant_id AND s.user_id = a.user_id AND s.id = a.share_id
		WHERE a.tenant_id = $1 AND a.user_id = $2 AND a.ticket_hash = $3
		FOR UPDATE OF a`, tenant, user, hash).
		Scan(&accessID, &shareID, &ticketExp, &ticketUsed, &status, &expires, &jwe, &created, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, busy(fmt.Errorf("shl: file: %w", err))
	}
	res := Result{Known: true, ShareID: shareID, LastUpdated: created.UTC()}
	a := access{tx: tx, tenant: tenant, user: user, share: shareID, info: info}
	if userStatus != "active" || status != StatusActive || !now.Before(expires) || jwe == nil ||
		ticketUsed != nil || ticketExp == nil || !now.Before(*ticketExp) {
		res.Outcome = OutcomeInactive
		return res, a.audit(ctx, OutcomeInactive, http.StatusNotFound)
	}
	if _, err := tx.Exec(ctx, `UPDATE ohs_sync.share_accesses SET ticket_used_at = now()
		WHERE tenant_id = $1 AND user_id = $2 AND id = $3`, tenant, user, accessID); err != nil {
		return Result{}, fmt.Errorf("shl: use ticket: %w", err)
	}
	res.Outcome, res.JWE = OutcomeFile, *jwe
	return res, a.audit(ctx, OutcomeFile, http.StatusOK)
}

// access records what happened on one share inside the request's
// transaction.
type access struct {
	tx           pgx.Tx
	tenant, user uuid.UUID
	share        uuid.UUID
	info         AuditInfo
}

// InactiveEvery is how often a link that no longer works has a request
// recorded in its access history: anyone holding it can keep calling it,
// and the history is not theirs to fill. The audit log has every one.
const InactiveEvery = 24 * time.Hour

// finish stores the access row (with the reader's name, which stays out
// of the audit log), the audit event, and commits. An inactive request
// has its row only if the share has none from the last InactiveEvery.
func (a access) finish(ctx context.Context, recipient, outcome string, status int, ticketHash *string, ticketExp *time.Time) error {
	if _, err := a.tx.Exec(ctx, `INSERT INTO ohs_sync.share_accesses
		(tenant_id, user_id, share_id, recipient, outcome, ticket_hash, ticket_expires_at)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE $5 <> 'inactive' OR NOT EXISTS (SELECT 1 FROM ohs_sync.share_accesses
			WHERE tenant_id = $1 AND user_id = $2 AND share_id = $3 AND outcome = 'inactive' AND at > now() - $8::interval)`,
		a.tenant, a.user, a.share, recipient, outcome, ticketHash, ticketExp,
		strconv.FormatInt(int64(InactiveEvery/time.Second), 10)+" seconds"); err != nil {
		return fmt.Errorf("shl: record access: %w", err)
	}
	return a.audit(ctx, outcome, status)
}

// audit records the share.access event and commits.
func (a access) audit(ctx context.Context, outcome string, status int) error {
	user := a.user
	if err := audit.Record(ctx, a.tx, audit.Event{Tenant: a.tenant, User: &user, ActorType: audit.ShareLink,
		ActorID: a.share.String(), Action: audit.ShareAccess, Method: a.info.Method, Route: a.info.Route,
		Status: status, RequestID: a.info.RequestID,
		Detail: map[string]any{"share_id": a.share.String(), "outcome": outcome}}); err != nil {
		return err
	}
	if err := a.tx.Commit(ctx); err != nil {
		return fmt.Errorf("shl: commit: %w", err)
	}
	return nil
}
