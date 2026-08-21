-- Hutang and piutang. TASKS 1.11, R5.5-5.6, R5.8, R11.5.
--
-- Two things create a payable: an unpaid purchase, and the opening balance
-- carried in at go-live (R11.5). The business is switching systems mid-life, so
-- what it owes and is owed on day one has to be typed in rather than derived
-- from a purchase history that does not exist here.
--
-- Payments are append-only, so the outstanding balance is derived, never
-- stored -- the same reasoning as the FIFO layers in migration 005. A stored
-- balance is a number that can disagree with the payments beneath it, and the
-- first time a supplier disputes an amount, the payment list is the answer.
--
-- Aging and the reports themselves are TASKS 6.4-6.5. This is the ledger they
-- read.

-- +goose Up

CREATE TABLE payable (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id     TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    supplier_id   TEXT    NOT NULL REFERENCES supplier (id) ON DELETE RESTRICT,

    -- OPENING is the go-live carry-in (R11.5); PURCHASE is an unpaid invoice.
    source        TEXT    NOT NULL CHECK (source IN ('PURCHASE', 'OPENING')),
    -- The purchase behind it, where there is one. An opening balance has none
    -- by definition, which is exactly why it needs its own source value rather
    -- than a synthetic purchase nobody ever made.
    purchase_id   TEXT    REFERENCES purchase (id) ON DELETE RESTRICT,

    invoice_no    TEXT,
    amount_idr    INTEGER NOT NULL CHECK (amount_idr > 0),
    incurred_on   TEXT    NOT NULL CHECK (incurred_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- R5.8: due dates and aging. Nullable because a supplier may give no term,
    -- but the report has to be able to say "overdue" when there is one.
    due_date      TEXT    CHECK (due_date IS NULL
                                 OR due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    note          TEXT,
    created_by    TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,

    CHECK ((source = 'PURCHASE' AND purchase_id IS NOT NULL)
        OR (source = 'OPENING'  AND purchase_id IS NULL))
) STRICT;

CREATE INDEX idx_payable_entity_due ON payable (entity_id, due_date);
CREATE INDEX idx_payable_supplier ON payable (supplier_id);
CREATE UNIQUE INDEX idx_payable_purchase ON payable (purchase_id) WHERE purchase_id IS NOT NULL;

CREATE TABLE payable_payment (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    payable_id    TEXT    NOT NULL REFERENCES payable (id) ON DELETE RESTRICT,
    -- R5.8: partial payments. Positive is money going out; a negative is a
    -- correction to an over-recorded payment, appended rather than edited.
    amount_idr    INTEGER NOT NULL CHECK (amount_idr <> 0),
    paid_on       TEXT    NOT NULL CHECK (paid_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    method        TEXT    NOT NULL DEFAULT 'TUNAI' CHECK (method IN ('TUNAI', 'TRANSFER', 'GIRO', 'LAINNYA')),
    note          TEXT,
    created_by    TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_payable_payment_payable ON payable_payment (payable_id);

CREATE TABLE receivable (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id     TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    customer_id   TEXT    NOT NULL REFERENCES customer (id) ON DELETE RESTRICT,

    -- SALE arrives with credit sales in TASKS 2.8. OPENING is the go-live
    -- carry-in, which is what this migration is for.
    source        TEXT    NOT NULL CHECK (source IN ('SALE', 'OPENING')),
    -- Deliberately not a foreign key yet: the sale table does not exist until
    -- migration 009 (TASKS 2.1). Adding the constraint then is a forward
    -- migration, not a rewrite of this one.
    sale_id       TEXT,

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

CREATE INDEX idx_receivable_entity_due ON receivable (entity_id, due_date);
CREATE INDEX idx_receivable_customer ON receivable (customer_id);

CREATE TABLE receivable_payment (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    receivable_id TEXT    NOT NULL REFERENCES receivable (id) ON DELETE RESTRICT,
    amount_idr    INTEGER NOT NULL CHECK (amount_idr <> 0),
    paid_on       TEXT    NOT NULL CHECK (paid_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    method        TEXT    NOT NULL DEFAULT 'TUNAI' CHECK (method IN ('TUNAI', 'TRANSFER', 'GIRO', 'LAINNYA')),
    note          TEXT,
    created_by    TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_receivable_payment_receivable ON receivable_payment (receivable_id);

-- Outstanding balances, derived. Mirrors stock_layer_balance in migration 005:
-- the amount owed is the invoice minus what has been paid against it, computed
-- on read, never carried in a column that can drift.
CREATE VIEW payable_balance AS
SELECT
    p.id                                                   AS id,
    p.entity_id                                            AS entity_id,
    p.supplier_id                                          AS supplier_id,
    p.source                                               AS source,
    p.purchase_id                                          AS purchase_id,
    p.invoice_no                                           AS invoice_no,
    p.amount_idr                                           AS amount_idr,
    CAST(COALESCE(SUM(pay.amount_idr), 0) AS INTEGER)      AS paid_idr,
    CAST(p.amount_idr - COALESCE(SUM(pay.amount_idr), 0) AS INTEGER) AS outstanding_idr,
    p.incurred_on                                          AS incurred_on,
    p.due_date                                             AS due_date,
    p.note                                                 AS note
FROM payable p
LEFT JOIN payable_payment pay ON pay.payable_id = p.id
GROUP BY p.id;

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

-- +goose Down
DROP VIEW receivable_balance;
DROP VIEW payable_balance;
DROP INDEX idx_receivable_payment_receivable;
DROP TABLE receivable_payment;
DROP INDEX idx_receivable_customer;
DROP INDEX idx_receivable_entity_due;
DROP TABLE receivable;
DROP INDEX idx_payable_payment_payable;
DROP TABLE payable_payment;
DROP INDEX idx_payable_purchase;
DROP INDEX idx_payable_supplier;
DROP INDEX idx_payable_entity_due;
DROP TABLE payable;
