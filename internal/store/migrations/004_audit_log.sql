-- Audit log. INV-10: every edit to historical data is logged — who, when,
-- before, after.
--
-- The user declined period locking (R7.3). Nothing prevents October's figures
-- moving after October's money was split between family members, so this log is
-- the entire mitigation. SPEC §6 puts it plainly: make it queryable by period
-- and by actor, or it is decoration. Hence the indexes below.
--
-- Naming: SPEC §6 calls the audited row's columns entity_type and entity_id.
-- In this schema "entity" already means legal_entity — a company — so those
-- names would read as "which company" at every call site. They are record_type
-- and record_id here, and legal_entity_id is the company scope.

-- +goose Up

CREATE TABLE audit_log (
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    -- Nullable, and ON DELETE SET NULL: the trail outlives the account. A
    -- departed family member's edits must remain visible.
    actor_user_id   TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    occurred_at     INTEGER NOT NULL,
    -- Which company the change belongs to. Null for records that are not
    -- entity-scoped, such as the shared product catalogue (D-006).
    legal_entity_id TEXT    REFERENCES legal_entity (id) ON DELETE RESTRICT,
    record_type     TEXT    NOT NULL,
    record_id       TEXT    NOT NULL,
    action          TEXT    NOT NULL CHECK (action IN ('CREATE', 'UPDATE', 'DELETE', 'VOID', 'ADJUST')),
    -- JSON snapshots. before is null on CREATE, after is null on DELETE, and
    -- both are present on UPDATE — enforced in the service layer, because a
    -- row recording that something changed without saying from what is not an
    -- audit trail (INV-10).
    before_json     TEXT,
    after_json      TEXT,
    -- Required for ADJUST (R12.5) and VOID: a correction to a finalised
    -- transaction (INV-2) that nobody can explain later is the thing this
    -- table exists to prevent.
    reason          TEXT,
    -- Ties the change back to the request that made it (INV-6).
    client_request_id TEXT
) STRICT;

-- "Queryable by period and by actor" (SPEC §6).
CREATE INDEX idx_audit_occurred ON audit_log (occurred_at);
CREATE INDEX idx_audit_actor ON audit_log (actor_user_id, occurred_at);
CREATE INDEX idx_audit_record ON audit_log (record_type, record_id, occurred_at);
CREATE INDEX idx_audit_entity ON audit_log (legal_entity_id, occurred_at);

-- +goose Down
DROP INDEX idx_audit_entity;
DROP INDEX idx_audit_record;
DROP INDEX idx_audit_actor;
DROP INDEX idx_audit_occurred;
DROP TABLE audit_log;
