-- Sales, cash sessions, returns and voids. TASKS 2.1, 2.6-2.10.
--
-- A sale is a finalised transaction (INV-2): immutable once written, corrected
-- only by a void or a return. Triggers enforce it, as on purchases.
--
-- Decisions taken here that the specs left open, with their reasoning:
--
--   Discounts are stored as whole rupiah at both line and invoice level.
--   R9.1 named "discount" and nothing else. Percentages are resolved to rupiah
--   in the UI and never stored, because a stored percentage has to be
--   re-multiplied to be read and that is a second place for the total to stop
--   matching the sum of its parts (SPEC 2.2). An invoice-level discount is
--   allocated across the lines by money.Allocate so the parts still sum.
--
--   A void is possible until the cash session containing the sale is closed.
--   R12.3 said "same-day error" without defining the boundary. The session is
--   the boundary a cashier actually understands -- the till is counted and the
--   day is done -- and R9.8 gives us one anyway. After that a correction is a
--   return, which is a different document because the goods came back later.
--
--   A sales return carries BOTH its own business date and the original sale's.
--   Which period a cross-boundary return lands in is still undecided (SPEC 4.4,
--   TASKS 3.4). Storing both dates now makes that a query change later instead
--   of a migration against settled financial history.

-- +goose Up

-- R9.8. Open/close, opening float, end-of-day reconciliation. Also the void
-- window: a sale can be voided until its session closes.
CREATE TABLE cash_session (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id      TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    status         TEXT    NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'CLOSED')),
    opened_at      INTEGER NOT NULL,
    business_date  TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    opened_by      TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    opening_float_idr INTEGER NOT NULL DEFAULT 0 CHECK (opening_float_idr >= 0),

    closed_at      INTEGER,
    closed_by      TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    -- What was actually in the drawer at close, and what the books expected.
    -- Both stored: the difference is the figure the person closing signs off,
    -- and recomputing it later from a moving history would not be that figure.
    counted_cash_idr   INTEGER,
    expected_cash_idr  INTEGER,
    variance_idr       INTEGER,
    close_note     TEXT,
    created_at     INTEGER NOT NULL,

    CHECK ((status = 'OPEN'   AND closed_at IS NULL AND counted_cash_idr IS NULL)
        OR (status = 'CLOSED' AND closed_at IS NOT NULL AND counted_cash_idr IS NOT NULL
            AND expected_cash_idr IS NOT NULL AND variance_idr IS NOT NULL
            AND variance_idr = counted_cash_idr - expected_cash_idr))
) STRICT;

CREATE INDEX idx_cash_session_entity ON cash_session (entity_id, business_date);
-- At most one open session per company. Two open tills at this scale means
-- someone forgot to close yesterday's, and the Z-report would be nonsense.
CREATE UNIQUE INDEX idx_cash_session_open ON cash_session (entity_id) WHERE status = 'OPEN';

CREATE TABLE sale (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    -- R9.7. Which company sold decides the tax treatment and which omzet
    -- counter this turnover lands in.
    entity_id      TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    cash_session_id TEXT   REFERENCES cash_session (id) ON DELETE RESTRICT,
    customer_id    TEXT    REFERENCES customer (id) ON DELETE RESTRICT,

    invoice_no     TEXT    NOT NULL,
    occurred_at    INTEGER NOT NULL,
    business_date  TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),

    status         TEXT    NOT NULL DEFAULT 'FINAL' CHECK (status IN ('FINAL', 'VOID')),
    voided_at      INTEGER,
    voided_by      TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    void_reason    TEXT,

    -- R9.6. Whether a faktur pajak was issued to the buyer. Recorded on every
    -- sale because a PKP owes output PPN whether or not the buyer took one
    -- (SPEC 2.3) -- the two facts are independent and the system must not let
    -- them collapse into each other.
    faktur_issued  INTEGER NOT NULL DEFAULT 0 CHECK (faktur_issued IN (0, 1)),
    faktur_no      TEXT    CHECK (faktur_no IS NULL OR faktur_issued = 1),

    -- Whole rupiah throughout. gross is the sum of line subtotals before any
    -- discount; discount_idr is the invoice-level discount, already allocated
    -- across the lines below so the parts still sum to the whole.
    gross_idr      INTEGER NOT NULL CHECK (gross_idr >= 0),
    discount_idr   INTEGER NOT NULL DEFAULT 0 CHECK (discount_idr >= 0),
    -- PPN is zero until the tax engine lands (TASKS 5.3). The column exists now
    -- so the snapshot has somewhere to go without a migration against settled
    -- sales; nothing computes a rate here (INV-4).
    ppn_idr        INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),
    total_idr      INTEGER NOT NULL CHECK (total_idr >= 0),

    -- COGS from the actual layers drawn, never an average (SPEC 4.1). Stored
    -- as the sum of this sale's consumptions so the sales report does not have
    -- to re-aggregate them; the consumption rows remain the authority and the
    -- drill-down reads those.
    cogs_idr       INTEGER NOT NULL DEFAULT 0 CHECK (cogs_idr >= 0),

    is_credit      INTEGER NOT NULL DEFAULT 0 CHECK (is_credit IN (0, 1)),
    due_date       TEXT    CHECK (due_date IS NULL
                                  OR (is_credit = 1 AND due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]')),
    note           TEXT,
    created_by     TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,

    CHECK (total_idr = gross_idr - discount_idr + ppn_idr),
    CHECK ((status = 'VOID' AND voided_at IS NOT NULL AND void_reason IS NOT NULL)
        OR (status = 'FINAL' AND voided_at IS NULL))
) STRICT;

CREATE UNIQUE INDEX idx_sale_invoice ON sale (entity_id, invoice_no);
CREATE INDEX idx_sale_entity_date ON sale (entity_id, business_date);
CREATE INDEX idx_sale_session ON sale (cash_session_id);
CREATE INDEX idx_sale_customer ON sale (customer_id, business_date);

CREATE TABLE sale_line (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    sale_id        TEXT    NOT NULL REFERENCES sale (id) ON DELETE RESTRICT,
    product_id     TEXT    NOT NULL REFERENCES product (id) ON DELETE RESTRICT,
    -- INV-8, snapshotted like the purchase side. Which owner's margin this line
    -- lands in is decided when the sale is rung, not when the report is run:
    -- re-tagging a product later must not move settled money.
    owner_id       TEXT    REFERENCES owner (id) ON DELETE RESTRICT,

    qty            INTEGER NOT NULL CHECK (qty > 0),
    unit_price_idr INTEGER NOT NULL CHECK (unit_price_idr >= 0),
    gross_idr      INTEGER NOT NULL CHECK (gross_idr >= 0),
    -- Line discount as entered, plus this line's share of any invoice-level
    -- discount. Kept apart because the cashier gave one and the system derived
    -- the other, and a dispute needs to tell them apart.
    line_discount_idr    INTEGER NOT NULL DEFAULT 0 CHECK (line_discount_idr >= 0),
    alloc_discount_idr   INTEGER NOT NULL DEFAULT 0 CHECK (alloc_discount_idr >= 0),
    net_idr        INTEGER NOT NULL CHECK (net_idr >= 0),
    -- The cost of the layers this line actually drew.
    cogs_idr       INTEGER NOT NULL DEFAULT 0 CHECK (cogs_idr >= 0),
    created_at     INTEGER NOT NULL,

    CHECK (gross_idr = unit_price_idr * qty),
    CHECK (net_idr = gross_idr - line_discount_idr - alloc_discount_idr)
) STRICT;

CREATE INDEX idx_sale_line_sale ON sale_line (sale_id);
CREATE INDEX idx_sale_line_product ON sale_line (product_id);
CREATE INDEX idx_sale_line_owner ON sale_line (owner_id);

-- R9.10: recorded, not processed. The gateway is explicitly out of scope, so
-- what matters is that the till reconciles and the reference is findable.
CREATE TABLE sale_payment (
    id          TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    sale_id     TEXT    NOT NULL REFERENCES sale (id) ON DELETE RESTRICT,
    method      TEXT    NOT NULL CHECK (method IN ('TUNAI', 'TRANSFER', 'QRIS', 'KARTU', 'KREDIT')),
    amount_idr  INTEGER NOT NULL CHECK (amount_idr > 0),
    -- Free text: a transfer reference, a QRIS transaction id, whatever the
    -- cashier can copy off the phone. Costs nothing to capture and is the only
    -- way to chase a payment that never arrived.
    reference   TEXT,
    created_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_sale_payment_sale ON sale_payment (sale_id);

-- R12.1, D-010. Goods came back later; the stock returns to the layer it was
-- drawn from and the margin reverses at exactly the cost that was taken.
CREATE TABLE sale_return (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id      TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    sale_id        TEXT    NOT NULL REFERENCES sale (id) ON DELETE RESTRICT,
    occurred_at    INTEGER NOT NULL,
    -- The day the goods came back.
    business_date  TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- The day the sale happened, copied here deliberately. Whether a November
    -- return of an October sale reduces October or November is undecided
    -- (SPEC 4.4, TASKS 3.4). Holding both dates makes that a query change
    -- rather than a migration against money that has already been split.
    sale_business_date TEXT NOT NULL CHECK (sale_business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    reason         TEXT    NOT NULL CHECK (length(trim(reason)) > 0),
    refund_idr     INTEGER NOT NULL CHECK (refund_idr >= 0),
    cogs_reversed_idr INTEGER NOT NULL DEFAULT 0 CHECK (cogs_reversed_idr >= 0),
    refund_method  TEXT    NOT NULL DEFAULT 'TUNAI'
                           CHECK (refund_method IN ('TUNAI', 'TRANSFER', 'QRIS', 'KARTU', 'POTONG_PIUTANG')),
    created_by     TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_sale_return_sale ON sale_return (sale_id);
CREATE INDEX idx_sale_return_entity_date ON sale_return (entity_id, business_date);

CREATE TABLE sale_return_line (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    sale_return_id TEXT    NOT NULL REFERENCES sale_return (id) ON DELETE RESTRICT,
    sale_line_id   TEXT    NOT NULL REFERENCES sale_line (id) ON DELETE RESTRICT,
    qty            INTEGER NOT NULL CHECK (qty > 0),
    refund_idr     INTEGER NOT NULL CHECK (refund_idr >= 0),
    cogs_reversed_idr INTEGER NOT NULL DEFAULT 0 CHECK (cogs_reversed_idr >= 0),
    created_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_sale_return_line_return ON sale_return_line (sale_return_id);
CREATE INDEX idx_sale_return_line_line ON sale_return_line (sale_line_id);

-- The forward migration TASKS 2.1 owed migration 008: receivable.sale_id could
-- not reference a table that did not exist yet. SQLite cannot add a foreign key
-- to an existing column, so the table is rebuilt -- the documented approach,
-- and it is empty or nearly so at this point in the project's life.
CREATE TABLE receivable_new (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id     TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    customer_id   TEXT    NOT NULL REFERENCES customer (id) ON DELETE RESTRICT,
    source        TEXT    NOT NULL CHECK (source IN ('SALE', 'OPENING')),
    sale_id       TEXT    REFERENCES sale (id) ON DELETE RESTRICT,
    invoice_no    TEXT,
    amount_idr    INTEGER NOT NULL CHECK (amount_idr > 0),
    incurred_on   TEXT    NOT NULL CHECK (incurred_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    due_date      TEXT    CHECK (due_date IS NULL
                                 OR due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    note          TEXT,
    created_by    TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,

    CHECK ((source = 'SALE' AND sale_id IS NOT NULL)
        OR (source = 'OPENING' AND sale_id IS NULL))
) STRICT;

INSERT INTO receivable_new SELECT
    id, entity_id, customer_id, source, sale_id, invoice_no, amount_idr,
    incurred_on, due_date, note, created_by, created_at
FROM receivable;

DROP VIEW receivable_balance;
DROP INDEX idx_receivable_customer;
DROP INDEX idx_receivable_entity_due;
DROP TABLE receivable;
ALTER TABLE receivable_new RENAME TO receivable;

CREATE INDEX idx_receivable_entity_due ON receivable (entity_id, due_date);
CREATE INDEX idx_receivable_customer ON receivable (customer_id);
CREATE UNIQUE INDEX idx_receivable_sale ON receivable (sale_id) WHERE sale_id IS NOT NULL;

CREATE VIEW receivable_balance AS
SELECT
    r.id                                                   AS id,
    r.entity_id                                            AS entity_id,
    r.customer_id                                          AS customer_id,
    r.source                                               AS source,
    r.sale_id                                              AS sale_id,
    r.invoice_no                                           AS invoice_no,
    r.amount_idr                                           AS amount_idr,
    CAST(COALESCE(SUM(pay.amount_idr), 0) AS INTEGER)      AS paid_idr,
    CAST(r.amount_idr - COALESCE(SUM(pay.amount_idr), 0) AS INTEGER) AS outstanding_idr,
    r.incurred_on                                          AS incurred_on,
    r.due_date                                             AS due_date,
    r.note                                                 AS note
FROM receivable r
LEFT JOIN receivable_payment pay ON pay.receivable_id = r.id
GROUP BY r.id;

-- INV-2. A void flips status and records who and why; it never deletes, and the
-- lines stay exactly as rung so the trail reads as what happened.

-- +goose StatementBegin
CREATE TRIGGER sale_is_immutable_except_void
BEFORE UPDATE ON sale
WHEN OLD.status = 'VOID' OR NEW.status <> 'VOID'
BEGIN
    SELECT RAISE(ABORT, 'sale is immutable (INV-2): void it or issue a return, never an UPDATE');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sale_no_delete
BEFORE DELETE ON sale
BEGIN
    SELECT RAISE(ABORT, 'sale is immutable (INV-2): a sale that happened cannot stop having happened');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sale_line_is_immutable_update
BEFORE UPDATE ON sale_line
BEGIN
    SELECT RAISE(ABORT, 'sale_line is immutable (INV-2)');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sale_line_no_delete
BEFORE DELETE ON sale_line
BEGIN
    SELECT RAISE(ABORT, 'sale_line is immutable (INV-2)');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sale_return_is_immutable_update
BEFORE UPDATE ON sale_return
BEGIN
    SELECT RAISE(ABORT, 'sale_return is immutable (INV-2)');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER cash_session_closed_is_final
BEFORE UPDATE ON cash_session
WHEN OLD.status = 'CLOSED'
BEGIN
    SELECT RAISE(ABORT, 'cash session is closed and final (INV-2): the till has been counted');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER cash_session_closed_is_final;
DROP TRIGGER sale_return_is_immutable_update;
DROP TRIGGER sale_line_no_delete;
DROP TRIGGER sale_line_is_immutable_update;
DROP TRIGGER sale_no_delete;
DROP TRIGGER sale_is_immutable_except_void;
DROP INDEX idx_sale_return_line_line;
DROP INDEX idx_sale_return_line_return;
DROP TABLE sale_return_line;
DROP INDEX idx_sale_return_entity_date;
DROP INDEX idx_sale_return_sale;
DROP TABLE sale_return;
DROP INDEX idx_sale_payment_sale;
DROP TABLE sale_payment;
DROP INDEX idx_sale_line_owner;
DROP INDEX idx_sale_line_product;
DROP INDEX idx_sale_line_sale;
DROP TABLE sale_line;
DROP INDEX idx_receivable_sale;
DROP INDEX idx_sale_customer;
DROP INDEX idx_sale_session;
DROP INDEX idx_sale_entity_date;
DROP INDEX idx_sale_invoice;
DROP TABLE sale;
DROP INDEX idx_cash_session_open;
DROP INDEX idx_cash_session_entity;
DROP TABLE cash_session;
