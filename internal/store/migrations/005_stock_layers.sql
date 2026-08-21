-- FIFO inventory. TASKS 1.1, SPEC §3.1, INV-7, INV-8, INV-9.
--
-- Two tables, both append-only. A layer records stock arriving at a known cost;
-- a consumption records a slice of that layer leaving. Remaining quantity is
-- qty_in − Σ qty_out, derived on read and never stored — see the view at the
-- bottom, and note there is no balance column anywhere here.
--
-- Append-only is enforced by triggers rather than left to convention. This
-- follows the same reasoning as STRICT for INV-1 (D-007) and depguard for
-- domain purity: an invariant the family's monthly settlement depends on should
-- fail at the storage layer, not in a code review two years from now. A
-- correction is a compensating record (INV-2), so nothing legitimate needs an
-- UPDATE. If some future migration genuinely does, it drops the trigger
-- deliberately and says why — which is exactly the friction wanted.

-- +goose Up

-- One acquisition of stock: a batch that arrived at a known cost, at a known
-- time, attributed to exactly one owner.
CREATE TABLE stock_layer (
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id       TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    product_id      TEXT    NOT NULL REFERENCES product (id) ON DELETE RESTRICT,
    -- INV-8: exactly one owner attribution, and NULL is one of them — the
    -- company bucket, reported as its own line beside the named owners (R2.2).
    -- Nullable by requirement. It is never a slot for "not sure yet": a
    -- movement with ambiguous ownership is a bug, and the place it has to be
    -- caught is where the product's owner is resolved, before the insert.
    owner_id        TEXT    REFERENCES owner (id) ON DELETE RESTRICT,

    -- The instant, UTC unix seconds. FIFO order is oldest acquired_at first,
    -- ties broken by id — which is time-ordered because ids are UUIDv7, so the
    -- tiebreak resolves to insertion order (D-003, SPEC §3.3).
    acquired_at     INTEGER NOT NULL,
    -- The day it counts for, in the entity's zone, computed at write time
    -- (D-005, INV-5). SQLite's localtime modifier uses the server's zone, so
    -- this cannot be derived in SQL.
    business_date   TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),

    -- SPEC 3.1 also lists RETURN here, but D-010 was decided afterwards and
    -- says a sales return restores to the ORIGINAL layer rather than creating a
    -- new one at return-date cost. So a return never produces a layer, and
    -- leaving the value available would let someone reintroduce exactly the bug
    -- D-010 rules out: revenue reversed in full against a cost reversed at a
    -- different figure, quietly inventing margin. A pre-go-live return with no
    -- original layer is an ADJUSTMENT.
    source          TEXT    NOT NULL CHECK (source IN ('PURCHASE', 'TRANSFER_IN', 'ADJUSTMENT', 'OPENING')),
    -- The purchase, transfer, opname, or opening-balance document behind it.
    -- Not a foreign key: it points into whichever table `source` names, and
    -- those tables arrive over several migrations.
    source_doc_id   TEXT,

    -- A layer exists to bring stock in. Zero or negative is not a layer.
    qty_in          INTEGER NOT NULL CHECK (qty_in > 0),

    -- The total, net of creditable PPN — never a unit cost. Rp 100.000 over 7
    -- units is Rp 14.285,714…, and storing that rounded loses rupiah on every
    -- draw (SPEC §1). Per-unit is derived in internal/domain/fifo.
    cost_total_idr  INTEGER NOT NULL CHECK (cost_total_idr >= 0),

    -- INV-9. Whether the supplier actually handed over the faktur pajak, which
    -- is what decides whether the PPN paid was recoverable or is real cost
    -- (SPEC §3.2). Kept on the layer, not only on the purchase, because it is
    -- the justification for cost_total_idr and that has to survive to the
    -- audit. Without it a layer's cost is wrong by ~11% and every margin
    -- downstream is wrong with it.
    faktur_received INTEGER NOT NULL DEFAULT 0 CHECK (faktur_received IN (0, 1)),
    -- What was actually paid in PPN, recorded whether or not it was creditable,
    -- so the decision can be re-examined later (INV-10).
    ppn_paid_idr    INTEGER NOT NULL DEFAULT 0 CHECK (ppn_paid_idr >= 0),

    -- Captured, not acted on. FEFO and batch-expiry picking are out of scope
    -- (REQUIREMENTS §6.4, §8); consumption is strictly oldest-acquired-first.
    expiry_date     TEXT    CHECK (expiry_date IS NULL
                                   OR expiry_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at      INTEGER NOT NULL
) STRICT;

-- The FIFO candidate lookup, in the order it is consumed. Deliberately keyed
-- without owner_id in the leading position: the query loads every owner's
-- layers for the product and internal/domain/fifo does the scoping, so an
-- insufficient-stock error can say how much stock other owners hold.
CREATE INDEX idx_stock_layer_fifo ON stock_layer (entity_id, product_id, acquired_at, id);
CREATE INDEX idx_stock_layer_owner ON stock_layer (owner_id, entity_id, product_id);
-- Reversing a purchase (R12.2) needs the layers it created.
CREATE INDEX idx_stock_layer_source ON stock_layer (source, source_doc_id);

-- One draw against one layer. Appended, never an edit to the layer (INV-7).
CREATE TABLE stock_consumption (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    layer_id      TEXT    NOT NULL REFERENCES stock_layer (id) ON DELETE RESTRICT,

    -- The sale, transfer, or adjustment that caused the draw. This is what the
    -- margin drill-down walks: owner total → sale → the layers it consumed and
    -- what each cost (SPEC §4.2). Not a foreign key, for the same reason as
    -- source_doc_id above.
    movement_id   TEXT    NOT NULL,
    -- SALES_RETURN and PURCHASE_RETURN are spelled out rather than sharing one
    -- RETURN value: they move stock in opposite directions. A sales return puts
    -- goods back (negative qty_out, naming the draw it reverses); a purchase
    -- return sends goods back to the supplier and draws them out (positive).
    -- One word for both is how someone eventually books one as the other.
    movement_type TEXT    NOT NULL CHECK (movement_type IN
                          ('SALE', 'TRANSFER_OUT', 'ADJUSTMENT', 'SALES_RETURN', 'PURCHASE_RETURN')),

    qty_out       INTEGER NOT NULL,
    -- The slice of the layer's cost_total_idr this draw took. Not a quantity
    -- times a rounded unit cost — see internal/domain/fifo.
    cost_idr      INTEGER NOT NULL,

    occurred_at   INTEGER NOT NULL,
    business_date TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),

    -- D-010. A sales return restores stock to the layer it came from, as an
    -- appended row with a negative qty_out and cost — never as an edit to the
    -- layer. remaining = qty_in − Σ qty_out then needs no special case.
    --
    -- The reversal must name the draw it undoes: a "return" corresponding to no
    -- actual sale is a bug, not a stock increase. That the reversals against
    -- one draw cannot exceed what it took is a service-layer rule (D-010) —
    -- SQL cannot see the running total from inside a row check.
    reverses_id   TEXT    REFERENCES stock_consumption (id) ON DELETE RESTRICT,
    created_at    INTEGER NOT NULL,

    -- Sign coherence, stated once. A draw takes stock out and costs something;
    -- a reversal puts stock back and costs a negative. Neither shape can be
    -- written as the other, so no row can quietly increase stock without
    -- pointing at what it is undoing.
    CHECK (
        (reverses_id IS NULL     AND qty_out > 0 AND cost_idr >= 0)
        OR
        (reverses_id IS NOT NULL AND qty_out < 0 AND cost_idr <= 0)
    )
) STRICT;

CREATE INDEX idx_stock_consumption_layer ON stock_consumption (layer_id);
-- The drill-down: which layers did this sale draw from (SPEC §4.2).
CREATE INDEX idx_stock_consumption_movement ON stock_consumption (movement_id);
-- Margin over a period reads these rows directly; COGS is the actual layers
-- drawn, never an average (SPEC §4.1).
CREATE INDEX idx_stock_consumption_date ON stock_consumption (business_date);
-- Capping reversals against a draw (D-010).
CREATE INDEX idx_stock_consumption_reverses ON stock_consumption (reverses_id) WHERE reverses_id IS NOT NULL;

-- INV-7, made mechanical. A layer is never decremented in place and a draw is
-- never edited away; both would destroy the trail owner settlement is decided
-- on months later.

-- +goose StatementBegin
CREATE TRIGGER stock_layer_is_append_only_update
BEFORE UPDATE ON stock_layer
BEGIN
    SELECT RAISE(ABORT, 'stock_layer is append-only (INV-7): correct it with a compensating record, never an UPDATE');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER stock_layer_is_append_only_delete
BEFORE DELETE ON stock_layer
BEGIN
    SELECT RAISE(ABORT, 'stock_layer is append-only (INV-7): a layer that existed cannot stop having existed');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER stock_consumption_is_append_only_update
BEFORE UPDATE ON stock_consumption
BEGIN
    SELECT RAISE(ABORT, 'stock_consumption is append-only (INV-7): reverse the draw with a new row (D-010), never an UPDATE');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER stock_consumption_is_append_only_delete
BEFORE DELETE ON stock_consumption
BEGIN
    SELECT RAISE(ABORT, 'stock_consumption is append-only (INV-7): reverse the draw with a new row (D-010), never a DELETE');
END;
-- +goose StatementEnd

-- Remaining quantity, derived. SPEC §3.1: if a cached balance is ever wanted it
-- goes into a view, never a mutable column — and at 1,000 SKUs it is not
-- wanted, so this is only the derivation written once instead of in every
-- query. qty_consumed is what internal/domain/fifo.Layer takes.
CREATE VIEW stock_layer_balance AS
SELECT
    l.id                                          AS id,
    l.entity_id                                   AS entity_id,
    l.product_id                                  AS product_id,
    l.owner_id                                    AS owner_id,
    l.acquired_at                                 AS acquired_at,
    l.business_date                               AS business_date,
    l.source                                      AS source,
    l.source_doc_id                               AS source_doc_id,
    l.qty_in                                      AS qty_in,
    -- CAST so sqlc infers int64 rather than interface{}: it cannot type
    -- COALESCE(SUM(...)) on its own, and an untyped balance at the call site is
    -- a type assertion waiting to panic on a quantity nobody is watching.
    CAST(COALESCE(SUM(c.qty_out), 0) AS INTEGER)            AS qty_consumed,
    CAST(l.qty_in - COALESCE(SUM(c.qty_out), 0) AS INTEGER) AS qty_remaining,
    l.cost_total_idr                              AS cost_total_idr,
    l.faktur_received                             AS faktur_received,
    l.ppn_paid_idr                                AS ppn_paid_idr,
    l.expiry_date                                 AS expiry_date
FROM stock_layer l
LEFT JOIN stock_consumption c ON c.layer_id = l.id
GROUP BY l.id;

-- +goose Down
DROP VIEW stock_layer_balance;
DROP TRIGGER stock_consumption_is_append_only_delete;
DROP TRIGGER stock_consumption_is_append_only_update;
DROP TRIGGER stock_layer_is_append_only_delete;
DROP TRIGGER stock_layer_is_append_only_update;
DROP INDEX idx_stock_consumption_reverses;
DROP INDEX idx_stock_consumption_date;
DROP INDEX idx_stock_consumption_movement;
DROP INDEX idx_stock_consumption_layer;
DROP TABLE stock_consumption;
DROP INDEX idx_stock_layer_source;
DROP INDEX idx_stock_layer_owner;
DROP INDEX idx_stock_layer_fifo;
DROP TABLE stock_layer;
