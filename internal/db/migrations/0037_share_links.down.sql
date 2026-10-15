-- SPDX-License-Identifier: AGPL-3.0-only
DROP TABLE ohs_sync.share_accesses;
DROP TABLE ohs_sync.shares;
-- The audit log is append only: events of a share link stay, so the old
-- check is restored for new rows only.
ALTER TABLE ohs_core.audit_events DROP CONSTRAINT audit_events_actor_type_check;
ALTER TABLE ohs_core.audit_events ADD CONSTRAINT audit_events_actor_type_check
    CHECK (actor_type IN ('api_key', 'user_token', 'admin', 'system')) NOT VALID;
