-- Inter-company stock transfer. TASKS 4.1-4.6, SPEC 3.4, R4.
--
-- This is the flow Olsera gets wrong. It records the transaction and does not
-- move the stock, which is why the user's quantities are drifting from reality
-- today (REQUIREMENTS 2). Here the source consumption and the destination layer
-- are written in one database transaction or neither is written -- see
-- internal/service/transfer.go, which owns that boundary.
--
-- Nothing new is needed on the stock tables: migration 005 already allows
-- movement_type TRANSFER_OUT on a consumption and source TRANSFER_IN on a
-- layer. This migration adds only the document those rows point at.
--
-- Decisions taken here:
--
--   Owner attribution is carried across, not re-derived. The family member
--   whose goods these are does not change because the goods crossed a company
--   line (SPEC 3.4, INV-8). transfer_line snapshots the owner for the same
--   reason sale_line does: re-tagging a product later must not move settled
--   money.
--
--   PKP status is snapshotted on the header. is_pkp is a fact about a company
--   that can change -- the non-PKP entity crossing Rp 4.8B is the whole of
--   Phase 7 -- and the tax treatment of a transfer is a fact about the day it
--   happened (INV-3). Recomputing a 2026 transfer's direction from 2028's
--   registration status would rewrite history.
--
--   Both business dates are stored. The two companies each resolve dates in
--   their own timezone (D-005, INV-5). They are the same zone today and the
--   columns will hold the same value; the day they do not, one column cannot
--   answer for both.
--
--   No hutang or piutang rows. Decided with the user (D-014): a transfer
--   records what the receiving company owes at cost, and the two companies net
--   off against each other in their own report. Hutang and piutang stay about
--   suppliers and customers, and neither company appears in the other's aging.

-- +goose Up

CREATE TABLE transfer (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),

    from_entity_id TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    to_entity_id   TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,

    transfer_no    TEXT    NOT NULL,
    occurred_at    INTEGER NOT NULL,
    -- The day each side counts it on, in that company's own zone.
    business_date      TEXT NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    to_business_date   TEXT NOT NULL CHECK (to_business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),

    -- Snapshotted, not looked up. See the header note.
    from_is_pkp    INTEGER NOT NULL CHECK (from_is_pkp IN (0, 1)),
    to_is_pkp      INTEGER NOT NULL CHECK (to_is_pkp IN (0, 1)),

    -- R4.5. Set only on a non-PKP -> PKP transfer, and only by a person who was
    -- shown what it costs and said go ahead. Stored because "nobody told us"
    -- is otherwise unanswerable a year later, when the margin is already gone
    -- and the only trace is stock that carries no input credit.
    credit_loss_ack     INTEGER NOT NULL DEFAULT 0 CHECK (credit_loss_ack IN (0, 1)),
    -- What the acknowledgement was quoted: input PPN already paid on this stock
    -- that can never now be credited by anyone.
    forfeited_ppn_idr   INTEGER NOT NULL DEFAULT 0 CHECK (forfeited_ppn_idr >= 0),

    -- At cost, no markup (R4.3). This is the consumed cost of the source
    -- layers, so it is a derived figure, stored because it is the amount the
    -- receiving company owes and that must not move when a layer is later
    -- read differently.
    cost_total_idr INTEGER NOT NULL CHECK (cost_total_idr >= 0),
    -- PPN charged on the delivery. Non-zero only when the sender is PKP, which
    -- the CHECK below enforces rather than leaving to the service layer.
    ppn_idr        INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),
    -- What the receiving company owes the sending one: cost plus any PPN.
    -- D-014's net position is the sum of these, both directions.
    amount_idr     INTEGER NOT NULL CHECK (amount_idr >= 0),

    faktur_issued  INTEGER NOT NULL DEFAULT 0 CHECK (faktur_issued IN (0, 1)),
    faktur_no      TEXT    CHECK (faktur_no IS NULL OR faktur_issued = 1),

    note           TEXT,
    created_by     TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,

    -- Stock cannot cross to where it already is.
    CHECK (from_entity_id <> to_entity_id),
    CHECK (amount_idr = cost_total_idr + ppn_idr),
    -- SPEC 2.3: a non-PKP cannot charge PPN and cannot issue a faktur. Not a
    -- convention -- the storage layer refuses it.
    CHECK (from_is_pkp = 1 OR (ppn_idr = 0 AND faktur_issued = 0)),
    -- R4.5: the only direction that destroys input credit is the only one that
    -- can carry an acknowledgement, and it must carry one.
    CHECK ((from_is_pkp = 0 AND to_is_pkp = 1 AND credit_loss_ack = 1)
        OR ((from_is_pkp = 1 OR to_is_pkp = 0) AND credit_loss_ack = 0 AND forfeited_ppn_idr = 0))
) STRICT;

CREATE UNIQUE INDEX idx_transfer_no ON transfer (from_entity_id, transfer_no);
CREATE INDEX idx_transfer_from ON transfer (from_entity_id, business_date);
CREATE INDEX idx_transfer_to ON transfer (to_entity_id, to_business_date);

CREATE TABLE transfer_line (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    transfer_id    TEXT    NOT NULL REFERENCES transfer (id) ON DELETE RESTRICT,
    product_id     TEXT    NOT NULL REFERENCES product (id) ON DELETE RESTRICT,
    -- INV-8, carried across the boundary unchanged. NULL is the company bucket.
    owner_id       TEXT    REFERENCES owner (id) ON DELETE RESTRICT,

    qty            INTEGER NOT NULL CHECK (qty > 0),
    -- The cost the source layers actually gave up for this line.
    cost_total_idr INTEGER NOT NULL CHECK (cost_total_idr >= 0),
    ppn_idr        INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),
    -- Input PPN embedded in the source layers for this line, forfeited when the
    -- direction destroys credit. Informational; the header carries the total.
    forfeited_ppn_idr INTEGER NOT NULL DEFAULT 0 CHECK (forfeited_ppn_idr >= 0),

    -- The layer this line created in the receiving company. The other half of
    -- the movement is the stock_consumption rows, found by movement_id =
    -- transfer_id. Together they are the audit trail Olsera does not have.
    dest_layer_id  TEXT    NOT NULL REFERENCES stock_layer (id) ON DELETE RESTRICT,
    created_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_transfer_line_transfer ON transfer_line (transfer_id);
CREATE INDEX idx_transfer_line_product ON transfer_line (product_id);
CREATE INDEX idx_transfer_line_owner ON transfer_line (owner_id);

-- INV-2. A transfer that happened cannot stop having happened, and it cannot
-- be edited into a different one. A correction is a transfer the other way.

-- +goose StatementBegin
CREATE TRIGGER transfer_is_immutable_update
BEFORE UPDATE ON transfer
BEGIN
    SELECT RAISE(ABORT, 'transfer is immutable (INV-2): correct it with a transfer the other way, never an UPDATE');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transfer_no_delete
BEFORE DELETE ON transfer
BEGIN
    SELECT RAISE(ABORT, 'transfer is immutable (INV-2): the stock already moved');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transfer_line_is_immutable_update
BEFORE UPDATE ON transfer_line
BEGIN
    SELECT RAISE(ABORT, 'transfer_line is immutable (INV-2)');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transfer_line_no_delete
BEFORE DELETE ON transfer_line
BEGIN
    SELECT RAISE(ABORT, 'transfer_line is immutable (INV-2)');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER transfer_line_no_delete;
DROP TRIGGER transfer_line_is_immutable_update;
DROP TRIGGER transfer_no_delete;
DROP TRIGGER transfer_is_immutable_update;
DROP INDEX idx_transfer_line_owner;
DROP INDEX idx_transfer_line_product;
DROP INDEX idx_transfer_line_transfer;
DROP TABLE transfer_line;
DROP INDEX idx_transfer_to;
DROP INDEX idx_transfer_from;
DROP INDEX idx_transfer_no;
DROP TABLE transfer;
