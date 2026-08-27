-- Tax on sales: the snapshot, and the invariant that the parts sum to the whole.
-- TASKS 5.8, 5.4, 5.5. SPEC 2.2, 2.3, 2.4. INV-2, INV-3.
--
-- Why this rebuilds the sale table
--
-- Migration 009 wrote CHECK (total_idr = gross_idr - discount_idr + ppn_idr),
-- which is only true when the tax is ADDED to the price. Under inclusive
-- pricing -- the ordinary Indonesian shelf price, and the seeded default -- the
-- PPN is already inside the total, and that CHECK would force ppn_idr to zero
-- on every sale. The liability would then be recorded nowhere, which is exactly
-- the silent loss TASKS 5.4 is about.
--
-- The replacement is one CHECK that holds under both:
--
--     dpp_idr + ppn_idr = total_idr
--
-- Exclusive: the DPP is the base and the tax is added, so they sum to the
-- total. Inclusive: the DPP is the price divided back and the tax is the
-- remainder, so they sum to the price. Untaxed: the DPP is the whole amount and
-- the tax is nil. It is SPEC 2.2's property enforced by the database on every
-- row, which is worth a table rebuild while the system is still pre-go-live.
--
-- SQLite cannot alter a CHECK, so the table is rebuilt the documented way.
-- Foreign keys are deferred rather than disabled: PRAGMA foreign_keys is a
-- no-op inside a transaction and goose runs this migration in one, whereas
-- defer_foreign_keys works there and checks everything at COMMIT -- by which
-- point sale exists again and every sale_line, sale_payment, sale_return and
-- receivable still points at a real row.
--
-- Decisions taken here:
--
--   The snapshot copies the whole rule, it does not reference it. tax_rule_id
--   is recorded for provenance and is never joined to in order to compute
--   anything (INV-3). Changing a rate next year must not move a figure the
--   family has already settled money on, and a foreign key to live config is
--   how that happens by accident.
--
--   Every line of a taxed sale gets a sale_tax row, including exempt ones.
--   A row with ppn_idr = 0 and is_exempt = 1 says the rule was in force and
--   this line was outside it; no row at all would be indistinguishable from
--   nobody having looked. It also keeps the reconciliation exact: the sum of
--   the snapshot rows is the sale.
--
--   Sales returns reverse output PPN. SPEC 2.4 states the output side as the
--   tax on sales and says nothing about returns, but the input side already
--   nets purchase returns (migration 006), and a position report that nets one
--   side and not the other reports a liability the business does not have.

-- +goose Up

PRAGMA defer_foreign_keys = ON;

CREATE TABLE sale_new (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
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

    -- R9.6, SPEC 2.3. Whether the buyer took a faktur is independent of whether
    -- PPN is owed: a PKP owes output PPN on the delivery either way. The two
    -- must not collapse into each other, so they are two columns and the tax
    -- engine is not given this one.
    faktur_issued  INTEGER NOT NULL DEFAULT 0 CHECK (faktur_issued IN (0, 1)),
    faktur_no      TEXT    CHECK (faktur_no IS NULL OR faktur_issued = 1),

    gross_idr      INTEGER NOT NULL CHECK (gross_idr >= 0),
    discount_idr   INTEGER NOT NULL DEFAULT 0 CHECK (discount_idr >= 0),

    -- The taxable base. On an untaxed sale it is the whole total and carries no
    -- tax meaning; it is what keeps the invariant below true either way.
    dpp_idr        INTEGER NOT NULL DEFAULT 0 CHECK (dpp_idr >= 0),
    ppn_idr        INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),
    -- Snapshotted from the rule that priced this sale (INV-3): whether the
    -- prices on it already contained the PPN. A receipt reprinted next year has
    -- to break down the same way it did at the till, and by then the rule may
    -- have been closed and replaced.
    ppn_inclusive  INTEGER NOT NULL DEFAULT 0 CHECK (ppn_inclusive IN (0, 1)),

    total_idr      INTEGER NOT NULL CHECK (total_idr >= 0),
    cogs_idr       INTEGER NOT NULL DEFAULT 0 CHECK (cogs_idr >= 0),

    is_credit      INTEGER NOT NULL DEFAULT 0 CHECK (is_credit IN (0, 1)),
    due_date       TEXT    CHECK (due_date IS NULL
                                  OR (is_credit = 1 AND due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]')),
    note           TEXT,
    created_by     TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,

    -- SPEC 2.2, on every row. The receipt adds up or the row does not exist.
    CHECK (dpp_idr + ppn_idr = total_idr),
    -- And the total is still what the customer was asked for: the labels less
    -- any discount, plus the tax only when the tax was added rather than
    -- already inside them.
    CHECK (total_idr = gross_idr - discount_idr
                     + (CASE WHEN ppn_inclusive = 1 THEN 0 ELSE ppn_idr END)),
    CHECK ((status = 'VOID' AND voided_at IS NOT NULL AND void_reason IS NOT NULL)
        OR (status = 'FINAL' AND voided_at IS NULL))
) STRICT;

-- Sales rung before the tax engine landed carry no PPN, so the whole total is
-- the DPP and the prices were neither inclusive nor exclusive of anything.
INSERT INTO sale_new (
    id, entity_id, cash_session_id, customer_id, invoice_no, occurred_at,
    business_date, status, voided_at, voided_by, void_reason,
    faktur_issued, faktur_no, gross_idr, discount_idr,
    dpp_idr, ppn_idr, ppn_inclusive, total_idr, cogs_idr,
    is_credit, due_date, note, created_by, created_at
)
SELECT
    id, entity_id, cash_session_id, customer_id, invoice_no, occurred_at,
    business_date, status, voided_at, voided_by, void_reason,
    faktur_issued, faktur_no, gross_idr, discount_idr,
    total_idr, ppn_idr, 0, total_idr, cogs_idr,
    is_credit, due_date, note, created_by, created_at
FROM sale;

DROP TRIGGER sale_is_immutable_except_void;
DROP TRIGGER sale_no_delete;
DROP INDEX idx_sale_customer;
DROP INDEX idx_sale_session;
DROP INDEX idx_sale_entity_date;
DROP INDEX idx_sale_invoice;
DROP TABLE sale;
ALTER TABLE sale_new RENAME TO sale;

CREATE UNIQUE INDEX idx_sale_invoice ON sale (entity_id, invoice_no);
CREATE INDEX idx_sale_entity_date ON sale (entity_id, business_date);
CREATE INDEX idx_sale_session ON sale (cash_session_id);
CREATE INDEX idx_sale_customer ON sale (customer_id, business_date);
-- SPEC 2.4's output side, split by whether a faktur was issued: the walk-in
-- half is the figure TASKS 5.4 exists to make visible.
CREATE INDEX idx_sale_faktur ON sale (entity_id, business_date, faktur_issued);

-- INV-2, recreated exactly as migration 009 had them: a void flips status and
-- records who and why; it never deletes, and the lines stay as rung.

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

-- TASKS 5.5, enforced where nothing can route around it. A non-PKP entity
-- charges no PPN and issues no faktur -- it legally cannot do either, and a
-- faktur number on a document the buyer takes away is a claim somebody else
-- will try to credit.

-- +goose StatementBegin
CREATE TRIGGER sale_non_pkp_charges_no_ppn
BEFORE INSERT ON sale
WHEN (NEW.ppn_idr <> 0 OR NEW.faktur_issued = 1)
 AND (SELECT is_pkp FROM legal_entity WHERE id = NEW.entity_id) = 0
BEGIN
    SELECT RAISE(ABORT, 'a non-PKP entity charges no PPN and issues no faktur (SPEC 2.3)');
END;
-- +goose StatementEnd

-- sale_line carries the same split, for one reason: margin.
--
-- COGS comes off a stock layer that is already net of creditable PPN
-- (SPEC 3.2), so revenue has to be net of PPN too or the two are not
-- comparable. Under inclusive pricing net_idr contains the PPN, and reading it
-- as revenue would overstate every owner's margin by 11% -- in the one report
-- family members settle real money on, monthly. dpp_idr is what the margin
-- report reads.
--
-- Rebuilt rather than ALTERed so the backfill is right: a line rung before the
-- tax engine landed carried no PPN, so its whole net is its DPP. ADD COLUMN
-- would default those rows to zero and silently wipe the revenue out of every
-- historical margin figure, and sale_line is immutable (INV-2) so no UPDATE
-- could put it back.

CREATE TABLE sale_line_new (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    sale_id        TEXT    NOT NULL REFERENCES sale (id) ON DELETE RESTRICT,
    product_id     TEXT    NOT NULL REFERENCES product (id) ON DELETE RESTRICT,
    owner_id       TEXT    REFERENCES owner (id) ON DELETE RESTRICT,

    qty            INTEGER NOT NULL CHECK (qty > 0),
    unit_price_idr INTEGER NOT NULL CHECK (unit_price_idr >= 0),
    gross_idr      INTEGER NOT NULL CHECK (gross_idr >= 0),
    line_discount_idr    INTEGER NOT NULL DEFAULT 0 CHECK (line_discount_idr >= 0),
    alloc_discount_idr   INTEGER NOT NULL DEFAULT 0 CHECK (alloc_discount_idr >= 0),
    net_idr        INTEGER NOT NULL CHECK (net_idr >= 0),

    -- The taxable base for this line, and the PPN on it. Revenue for the margin
    -- report is dpp_idr, always -- under either pricing mode and whether or not
    -- any tax was levied.
    dpp_idr        INTEGER NOT NULL DEFAULT 0 CHECK (dpp_idr >= 0),
    ppn_idr        INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),

    cogs_idr       INTEGER NOT NULL DEFAULT 0 CHECK (cogs_idr >= 0),
    created_at     INTEGER NOT NULL,

    CHECK (gross_idr = unit_price_idr * qty),
    CHECK (net_idr = gross_idr - line_discount_idr - alloc_discount_idr),
    -- The two pricing modes, and nothing between them. Inclusive: the PPN is
    -- inside the net, so the two parts sum to it. Exclusive: the net IS the
    -- base and the PPN is added on top. Which of the two applied is on the
    -- sale, as ppn_inclusive; the authoritative sum-check is there.
    CHECK (dpp_idr + ppn_idr = net_idr OR dpp_idr = net_idr)
) STRICT;

INSERT INTO sale_line_new (
    id, sale_id, product_id, owner_id, qty, unit_price_idr, gross_idr,
    line_discount_idr, alloc_discount_idr, net_idr, dpp_idr, ppn_idr,
    cogs_idr, created_at
)
SELECT
    id, sale_id, product_id, owner_id, qty, unit_price_idr, gross_idr,
    line_discount_idr, alloc_discount_idr, net_idr, net_idr, 0,
    cogs_idr, created_at
FROM sale_line;

DROP TRIGGER sale_line_is_immutable_update;
DROP TRIGGER sale_line_no_delete;
DROP INDEX idx_sale_line_owner;
DROP INDEX idx_sale_line_product;
DROP INDEX idx_sale_line_sale;
DROP TABLE sale_line;
ALTER TABLE sale_line_new RENAME TO sale_line;

CREATE INDEX idx_sale_line_sale ON sale_line (sale_id);
CREATE INDEX idx_sale_line_product ON sale_line (product_id);
CREATE INDEX idx_sale_line_owner ON sale_line (owner_id);

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

-- The tax snapshot. One row per sale line of a taxed sale, holding the rule
-- that priced it rather than a pointer to config that can change (INV-3).
CREATE TABLE sale_tax (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    sale_id        TEXT    NOT NULL REFERENCES sale (id) ON DELETE RESTRICT,
    sale_line_id   TEXT    NOT NULL REFERENCES sale_line (id) ON DELETE RESTRICT,

    -- The rule, copied whole. Every column here is a fact about the day of the
    -- sale, not a lookup (INV-3, TASKS 5.11).
    tax_type          TEXT    NOT NULL CHECK (tax_type IN ('PPN', 'PPNBM')),
    rate_bp           INTEGER NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000),
    dpp_factor_num    INTEGER NOT NULL CHECK (dpp_factor_num > 0),
    dpp_factor_den    INTEGER NOT NULL CHECK (dpp_factor_den > 0),
    is_inclusive      INTEGER NOT NULL CHECK (is_inclusive IN (0, 1)),
    calculation_level TEXT    NOT NULL CHECK (calculation_level IN ('LINE', 'INVOICE')),
    rounding_mode     TEXT    NOT NULL,
    rounding_unit     INTEGER NOT NULL CHECK (rounding_unit >= 1),
    -- The citation, copied too. A faktur queried in three years has to be
    -- answerable with the regulation that was in force, not the current one.
    legal_ref         TEXT    NOT NULL CHECK (length(trim(legal_ref)) > 0),
    -- Provenance only. Deliberately nullable and ON DELETE SET NULL: losing the
    -- config row must not take the snapshot with it.
    tax_rule_id       TEXT    REFERENCES tax_rule (id) ON DELETE SET NULL,

    -- This line was outside the rule that was in force. Recorded rather than
    -- omitted, so an exempt line is distinguishable from a line nobody looked
    -- at.
    is_exempt      INTEGER NOT NULL DEFAULT 0 CHECK (is_exempt IN (0, 1)),

    dpp_idr        INTEGER NOT NULL CHECK (dpp_idr >= 0),
    ppn_idr        INTEGER NOT NULL CHECK (ppn_idr >= 0),
    created_at     INTEGER NOT NULL,

    CHECK (is_exempt = 0 OR ppn_idr = 0)
) STRICT;

-- One snapshot per line per tax.
CREATE UNIQUE INDEX idx_sale_tax_line ON sale_tax (sale_line_id, tax_type);
CREATE INDEX idx_sale_tax_sale ON sale_tax (sale_id);

-- +goose StatementBegin
CREATE TRIGGER sale_tax_is_immutable_update
BEFORE UPDATE ON sale_tax
BEGIN
    SELECT RAISE(ABORT, 'a tax snapshot is what the law said on the day (INV-3): it is never updated');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sale_tax_no_delete
BEFORE DELETE ON sale_tax
BEGIN
    SELECT RAISE(ABORT, 'a tax snapshot is what the law said on the day (INV-3): it is never deleted');
END;
-- +goose StatementEnd

-- Returns give back output PPN, mirroring purchase returns on the input side.
ALTER TABLE sale_return ADD COLUMN ppn_reversed_idr INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sale_return_line ADD COLUMN ppn_reversed_idr INTEGER NOT NULL DEFAULT 0;

-- +goose Down
--
-- Migrations are forward-only in this project (CLAUDE.md); this exists so
-- `goose down` on a development database does not leave a half-migrated file.
-- It reverses the shape, not the data: a sale rung with inclusive PPN cannot
-- satisfy migration 009's CHECK, so rolling back after tax has been charged
-- will fail here rather than silently drop the liability. That is the correct
-- failure -- restore from a backup instead.
PRAGMA defer_foreign_keys = ON;

ALTER TABLE sale_return_line DROP COLUMN ppn_reversed_idr;
ALTER TABLE sale_return DROP COLUMN ppn_reversed_idr;
DROP TRIGGER sale_tax_no_delete;
DROP TRIGGER sale_tax_is_immutable_update;
DROP INDEX idx_sale_tax_sale;
DROP INDEX idx_sale_tax_line;
DROP TABLE sale_tax;

CREATE TABLE sale_line_old (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    sale_id        TEXT    NOT NULL REFERENCES sale (id) ON DELETE RESTRICT,
    product_id     TEXT    NOT NULL REFERENCES product (id) ON DELETE RESTRICT,
    owner_id       TEXT    REFERENCES owner (id) ON DELETE RESTRICT,
    qty            INTEGER NOT NULL CHECK (qty > 0),
    unit_price_idr INTEGER NOT NULL CHECK (unit_price_idr >= 0),
    gross_idr      INTEGER NOT NULL CHECK (gross_idr >= 0),
    line_discount_idr    INTEGER NOT NULL DEFAULT 0 CHECK (line_discount_idr >= 0),
    alloc_discount_idr   INTEGER NOT NULL DEFAULT 0 CHECK (alloc_discount_idr >= 0),
    net_idr        INTEGER NOT NULL CHECK (net_idr >= 0),
    cogs_idr       INTEGER NOT NULL DEFAULT 0 CHECK (cogs_idr >= 0),
    created_at     INTEGER NOT NULL,
    CHECK (gross_idr = unit_price_idr * qty),
    CHECK (net_idr = gross_idr - line_discount_idr - alloc_discount_idr)
) STRICT;

INSERT INTO sale_line_old SELECT
    id, sale_id, product_id, owner_id, qty, unit_price_idr, gross_idr,
    line_discount_idr, alloc_discount_idr, net_idr, cogs_idr, created_at
FROM sale_line;

DROP TRIGGER sale_line_no_delete;
DROP TRIGGER sale_line_is_immutable_update;
DROP INDEX idx_sale_line_owner;
DROP INDEX idx_sale_line_product;
DROP INDEX idx_sale_line_sale;
DROP TABLE sale_line;
ALTER TABLE sale_line_old RENAME TO sale_line;

CREATE INDEX idx_sale_line_sale ON sale_line (sale_id);
CREATE INDEX idx_sale_line_product ON sale_line (product_id);
CREATE INDEX idx_sale_line_owner ON sale_line (owner_id);

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

DROP TRIGGER sale_non_pkp_charges_no_ppn;
DROP TRIGGER sale_no_delete;
DROP TRIGGER sale_is_immutable_except_void;
DROP INDEX idx_sale_faktur;
DROP INDEX idx_sale_customer;
DROP INDEX idx_sale_session;
DROP INDEX idx_sale_entity_date;
DROP INDEX idx_sale_invoice;

CREATE TABLE sale_old (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
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
    faktur_issued  INTEGER NOT NULL DEFAULT 0 CHECK (faktur_issued IN (0, 1)),
    faktur_no      TEXT    CHECK (faktur_no IS NULL OR faktur_issued = 1),
    gross_idr      INTEGER NOT NULL CHECK (gross_idr >= 0),
    discount_idr   INTEGER NOT NULL DEFAULT 0 CHECK (discount_idr >= 0),
    ppn_idr        INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),
    total_idr      INTEGER NOT NULL CHECK (total_idr >= 0),
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

INSERT INTO sale_old SELECT
    id, entity_id, cash_session_id, customer_id, invoice_no, occurred_at,
    business_date, status, voided_at, voided_by, void_reason, faktur_issued,
    faktur_no, gross_idr, discount_idr, ppn_idr, total_idr, cogs_idr,
    is_credit, due_date, note, created_by, created_at
FROM sale;

DROP TABLE sale;
ALTER TABLE sale_old RENAME TO sale;

CREATE UNIQUE INDEX idx_sale_invoice ON sale (entity_id, invoice_no);
CREATE INDEX idx_sale_entity_date ON sale (entity_id, business_date);
CREATE INDEX idx_sale_session ON sale (cash_session_id);
CREATE INDEX idx_sale_customer ON sale (customer_id, business_date);

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
