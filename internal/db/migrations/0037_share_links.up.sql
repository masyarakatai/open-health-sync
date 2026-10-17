-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Share links (SMART Health Links, docs/share-links.md): a read-only snapshot
-- of some of a user's own data, encrypted with a key the instance never
-- stores. shares holds one row per link: the SHA-256 of its manifest id, what
-- it holds (categories, time window, the provider/category pairs in sources),
-- its expiry, the PBKDF2 hash of its passcode and the count of wrong ones, its
-- state, and the encrypted file (jwe, NULL once revoked, disabled or swept
-- after expiry). share_accesses is the access history the person reads: one
-- row per manifest request on a known link (one a day at most once the link
-- no longer works), with the name the reader gave, and the single use ticket
-- of a file served by location. Both go with the user on erasure; a provider
-- erasure keeps them and revokes the links that hold that provider's records.
CREATE TABLE ohs_sync.shares (
    id                uuid        PRIMARY KEY,
    tenant_id         uuid        NOT NULL,
    user_id           uuid        NOT NULL,
    manifest_hash     text        NOT NULL,
    label             text        NOT NULL DEFAULT '',
    categories        text[]      NOT NULL,
    data_start        timestamptz NOT NULL,
    data_end          timestamptz NOT NULL,
    expires_at        timestamptz NOT NULL,
    passcode_hash     text,
    passcode_failures integer     NOT NULL DEFAULT 0,
    status            text        NOT NULL DEFAULT 'active',
    revoked_reason    text,
    revoked_at        timestamptz,
    jwe               text,
    file_bytes        bigint      NOT NULL,
    observations      integer     NOT NULL,
    sources           text[]      NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shares_manifest_hash_key UNIQUE (manifest_hash),
    CONSTRAINT shares_status_check CHECK (status IN ('active', 'revoked', 'disabled')),
    CONSTRAINT shares_revoked_reason_check
        CHECK (revoked_reason IN ('owner', 'consent', 'provider_erased', 'passcode_attempts')),
    CONSTRAINT shares_window_check CHECK (data_end > data_start)
);
CREATE INDEX shares_user ON ohs_sync.shares (tenant_id, user_id, created_at, id);
CREATE INDEX shares_expired_file ON ohs_sync.shares (expires_at) WHERE jwe IS NOT NULL;

CREATE TABLE ohs_sync.share_accesses (
    id                bigserial   PRIMARY KEY,
    tenant_id         uuid        NOT NULL,
    user_id           uuid        NOT NULL,
    share_id          uuid        NOT NULL REFERENCES ohs_sync.shares (id) ON DELETE CASCADE,
    at                timestamptz NOT NULL DEFAULT now(),
    recipient         text        NOT NULL,
    outcome           text        NOT NULL,
    ticket_hash       text,
    ticket_expires_at timestamptz,
    ticket_used_at    timestamptz,
    CONSTRAINT share_accesses_outcome_check CHECK (outcome IN ('served', 'wrong_passcode', 'inactive'))
);
CREATE INDEX share_accesses_share ON ohs_sync.share_accesses (tenant_id, user_id, share_id, id);
-- The foreign key's own index: deleting a share looks its accesses up by share_id.
CREATE INDEX share_accesses_share_id ON ohs_sync.share_accesses (share_id);
CREATE UNIQUE INDEX share_accesses_ticket ON ohs_sync.share_accesses (ticket_hash) WHERE ticket_hash IS NOT NULL;

-- The audit log names a share link as the actor of an access.
ALTER TABLE ohs_core.audit_events DROP CONSTRAINT audit_events_actor_type_check;
ALTER TABLE ohs_core.audit_events ADD CONSTRAINT audit_events_actor_type_check
    CHECK (actor_type IN ('api_key', 'user_token', 'admin', 'system', 'share_link'));
