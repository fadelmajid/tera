-- Stock opname: physical count, variance, adjustment posting. TASKS 1.10,
-- R12.4-5.
--
-- Required at go-live, and periodically after, because the quantities in the
-- current system are already drifting from reality -- Olsera records an
-- inter-company transaction without moving the stock (REQUIREMENTS §9 item 2).
-- The first opname is how the drift gets measured rather than inherited.
--
-- The count is recorded first and posted second, deliberately. Counting a shop
-- takes hours; posting is the moment stock actually moves. Keeping them apart
-- means a half-finished count is not half-applied to the books.

-- +goose Up

CREATE TABLE stock_opname (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id     TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    -- DRAFT while counting, POSTED once the adjustments have been written.
    -- POSTED is terminal: the adjustments are stock movements and stock
    -- movements are append-only (INV-7).
    status        TEXT    NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'POSTED')),
    counted_at    INTEGER NOT NULL,
    business_date TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    note          TEXT,
    posted_at     INTEGER,
    posted_by     TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_by    TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,

    CHECK ((status = 'POSTED' AND posted_at IS NOT NULL) OR (status = 'DRAFT' AND posted_at IS NULL))
) STRICT;

CREATE INDEX idx_stock_opname_entity ON stock_opname (entity_id, business_date);

CREATE TABLE stock_opname_line (
    id          TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    opname_id   TEXT    NOT NULL REFERENCES stock_opname (id) ON DELETE CASCADE,
    product_id  TEXT    NOT NULL REFERENCES product (id) ON DELETE RESTRICT,
    -- INV-8. A count is per owner, not per product: Budi's 3 boxes and Sari's
    -- 40 of the same item are separate lines, because a variance has to land in
    -- exactly one person's bucket. NULL is the company bucket (R2.2).
    owner_id    TEXT    REFERENCES owner (id) ON DELETE RESTRICT,

    -- What the books said at the moment of counting, snapshotted. Recomputing
    -- it at posting time would silently absorb any sale rung in between and
    -- report a variance of zero.
    system_qty  INTEGER NOT NULL,
    counted_qty INTEGER NOT NULL CHECK (counted_qty >= 0),
    -- Stored rather than derived on read: it is the number the person signing
    -- off actually saw, and it must not move afterwards.
    variance    INTEGER NOT NULL,

    -- R12.5. Every adjustment carries a reason code, and a variance that nobody
    -- explained is exactly the kind of thing this system exists to stop being
    -- normal. Required on any line that is not flat.
    reason_code TEXT    CHECK (reason_code IS NULL OR reason_code IN
                        ('RUSAK', 'HILANG', 'KADALUARSA', 'SALAH_CATAT', 'RETUR_TIDAK_TERCATAT', 'LAINNYA')),
    reason_note TEXT,

    -- A surplus needs a cost, because it becomes a stock layer and every layer
    -- has to be able to explain what it cost (SPEC §3.2). The screen defaults
    -- it from the most recent layer of the same product and owner -- found
    -- stock is nearly always a miscounted recent delivery -- but the figure is
    -- stored explicitly rather than derived at posting time, so the person
    -- signing the count sees the number that will hit the books. Zero is a
    -- legitimate answer (free samples turning up); NULL means not applicable.
    --
    -- A shortfall does not use it: those units cost whatever the layers they
    -- came out of cost, which FIFO already knows.
    unit_cost_idr INTEGER CHECK (unit_cost_idr IS NULL OR unit_cost_idr >= 0),
    created_at  INTEGER NOT NULL,

    CHECK (variance = counted_qty - system_qty),
    CHECK (variance = 0 OR reason_code IS NOT NULL),
    CHECK (variance <= 0 OR unit_cost_idr IS NOT NULL),
    -- One line per product per owner, or two counts of the same shelf disagree
    -- and nothing decides which wins.
    UNIQUE (opname_id, product_id, owner_id)
) STRICT;

CREATE INDEX idx_stock_opname_line_opname ON stock_opname_line (opname_id);
CREATE INDEX idx_stock_opname_line_product ON stock_opname_line (product_id);

-- What a posted line actually did to the books. A surplus creates a layer; a
-- shortfall draws layers down oldest-first. Recorded so the movement can be
-- traced back to the count that ordered it.
CREATE TABLE stock_opname_posting (
    id              TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    opname_line_id  TEXT    NOT NULL REFERENCES stock_opname_line (id) ON DELETE RESTRICT,
    -- Exactly one of these: a surplus names the layer it created, a shortfall
    -- names a consumption that drew stock out.
    stock_layer_id  TEXT    REFERENCES stock_layer (id) ON DELETE RESTRICT,
    consumption_id  TEXT    REFERENCES stock_consumption (id) ON DELETE RESTRICT,
    qty             INTEGER NOT NULL CHECK (qty > 0),
    cost_idr        INTEGER NOT NULL,
    created_at      INTEGER NOT NULL,

    CHECK ((stock_layer_id IS NOT NULL AND consumption_id IS NULL)
        OR (stock_layer_id IS NULL AND consumption_id IS NOT NULL))
) STRICT;

CREATE INDEX idx_stock_opname_posting_line ON stock_opname_posting (opname_line_id);

-- A posted opname is a finalised correction (INV-2). Draft lines may still be
-- edited while counting, which is why only the posted header is frozen.

-- +goose StatementBegin
CREATE TRIGGER stock_opname_posted_is_final
BEFORE UPDATE ON stock_opname
WHEN OLD.status = 'POSTED'
BEGIN
    SELECT RAISE(ABORT, 'stock opname is posted and final (INV-2): count again rather than editing this one');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER stock_opname_line_posted_is_final
BEFORE UPDATE ON stock_opname_line
WHEN (SELECT status FROM stock_opname WHERE id = OLD.opname_id) = 'POSTED'
BEGIN
    SELECT RAISE(ABORT, 'stock opname is posted and final (INV-2): its lines justify stock movements already written');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER stock_opname_line_posted_no_delete
BEFORE DELETE ON stock_opname_line
WHEN (SELECT status FROM stock_opname WHERE id = OLD.opname_id) = 'POSTED'
BEGIN
    SELECT RAISE(ABORT, 'stock opname is posted and final (INV-2): its lines justify stock movements already written');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER stock_opname_line_posted_no_delete;
DROP TRIGGER stock_opname_line_posted_is_final;
DROP TRIGGER stock_opname_posted_is_final;
DROP INDEX idx_stock_opname_posting_line;
DROP TABLE stock_opname_posting;
DROP INDEX idx_stock_opname_line_product;
DROP INDEX idx_stock_opname_line_opname;
DROP TABLE stock_opname_line;
DROP INDEX idx_stock_opname_entity;
DROP TABLE stock_opname;
