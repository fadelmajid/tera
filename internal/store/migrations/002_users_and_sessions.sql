-- Users, per-entity roles, and sessions. R13.
--
-- Roles exist from day one, all three (D-004). R7.1 turns on the
-- staff/manager distinction — staff cannot edit past transactions — so
-- deferring the role would mean retrofitting authorization into every mutating
-- handler later.

-- +goose Up

-- Named app_user rather than user: "user" is a reserved word in several
-- engines, and D-006's note about keeping a hosted tier possible costs nothing
-- to honour here.
CREATE TABLE app_user (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    username      TEXT    NOT NULL UNIQUE,
    full_name     TEXT    NOT NULL,
    -- bcrypt. The plaintext is never stored and never logged.
    password_hash TEXT    NOT NULL,
    is_active     INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

-- R13.4: access is scoped per company, and a user may hold a different role in
-- each. One row per (user, entity) — the primary key says a user has exactly
-- one role in a given company, which is what makes authorization decidable.
CREATE TABLE user_entity_role (
    user_id    TEXT    NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    entity_id  TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    role       TEXT    NOT NULL CHECK (role IN ('owner', 'manager', 'staff')),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, entity_id)
) STRICT;

CREATE INDEX idx_user_entity_role_entity ON user_entity_role (entity_id);

-- Sessions are server-side, not signed tokens. There is no key to rotate, no
-- clock skew to reason about, and revoking a session is a DELETE — which
-- matters when the machine holding the family's settlement figures is a PC in
-- a shop.
--
-- The token itself is never stored. Only its SHA-256, so a copy of the
-- database file — and R14 says copies of it exist on removable media — does
-- not hand anyone a working session.
CREATE TABLE session (
    token_hash   TEXT    NOT NULL PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_session_user ON session (user_id);
CREATE INDEX idx_session_expiry ON session (expires_at);

-- +goose Down
DROP INDEX idx_session_expiry;
DROP INDEX idx_session_user;
DROP TABLE session;
DROP INDEX idx_user_entity_role_entity;
DROP TABLE user_entity_role;
DROP TABLE app_user;
