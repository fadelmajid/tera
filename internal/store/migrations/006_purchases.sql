-- Purchases. TASKS 1.5-1.6, R10, SPEC §3.2, INV-9.
--
-- A purchase is what creates stock layers, and the single field faktur_received
-- is what decides their cost basis. Same supplier, same price, with a faktur and
-- without: two different costs, and therefore two different margins on the same
-- sale. That difference is invisible in the system the business uses today.
--
-- Finalised and immutable (INV-2), same as the stock tables: a correction is a
-- purchase return (R12.2), never an edit. Triggers enforce it.
--
-- What is NOT here: any computation of PPN from a rate. The amount recorded is
-- the amount on the supplier's invoice, typed in as it reads. Deriving it would
-- mean hardcoding 11%, which INV-4 forbids, and the effective-dated tax_rule
-- table does not arrive until TASKS 5.1. It is also simply more truthful --
-- what the supplier charged is a fact about the invoice, not a calculation.

-- +goose Up

CREATE TABLE purchase (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    -- R10.5. Which company bought is what decides whether the input PPN is
    -- creditable at all (SPEC §2.3), so it is not an afterthought here.
    entity_id     TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    supplier_id   TEXT    NOT NULL REFERENCES supplier (id) ON DELETE RESTRICT,

    invoice_no    TEXT,
    occurred_at   INTEGER NOT NULL,
    business_date TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),

    -- INV-9, and the reason this table exists. Whether the supplier actually
    -- handed over the faktur pajak -- not whether one was promised, invoiced, or
    -- is in the post. The paper is what makes the PPN creditable.
    faktur_received INTEGER NOT NULL DEFAULT 0 CHECK (faktur_received IN (0, 1)),
    -- The faktur number, captured for the owner's records and for whenever
    -- e-Faktur is dealt with. REQUIREMENTS §8: capture the fields, ship no
    -- integration. A number without the paper is not a received faktur, hence
    -- the check.
    faktur_no       TEXT    CHECK (faktur_no IS NULL OR faktur_received = 1),

    -- Snapshotted from the supplier's invoice, not recomputed later (INV-3 in
    -- spirit: a historical document does not change when config does).
    subtotal_idr  INTEGER NOT NULL CHECK (subtotal_idr >= 0),
    ppn_idr       INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),
    total_idr     INTEGER NOT NULL CHECK (total_idr >= 0),

    -- An unpaid purchase is hutang (R5.5). The payable row is created with the
    -- purchase, in the same transaction.
    is_credit     INTEGER NOT NULL DEFAULT 0 CHECK (is_credit IN (0, 1)),
    due_date      TEXT    CHECK (due_date IS NULL
                                 OR (is_credit = 1 AND due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]')),

    note          TEXT,
    created_by    TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,

    -- The invoice must add up. A header whose parts do not sum is a typo that
    -- would otherwise propagate into cost bases and margins.
    CHECK (total_idr = subtotal_idr + ppn_idr)
) STRICT;

CREATE INDEX idx_purchase_entity_date ON purchase (entity_id, business_date);
CREATE INDEX idx_purchase_supplier ON purchase (supplier_id, business_date);
-- SPEC §2.4's input side reads exactly this: purchases WHERE faktur_received.
CREATE INDEX idx_purchase_faktur ON purchase (entity_id, faktur_received, business_date);
CREATE UNIQUE INDEX idx_purchase_invoice ON purchase (supplier_id, invoice_no) WHERE invoice_no IS NOT NULL;

CREATE TABLE purchase_line (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    purchase_id    TEXT    NOT NULL REFERENCES purchase (id) ON DELETE RESTRICT,
    product_id     TEXT    NOT NULL REFERENCES product (id) ON DELETE RESTRICT,

    -- INV-8, and snapshotted deliberately. The owner is resolved from the
    -- product when the purchase is entered and frozen here, rather than joined
    -- to product.owner_id at report time. Re-tagging a product next year must
    -- not silently rewrite whose margin last year's stock belonged to -- the
    -- family has already settled that money.
    owner_id       TEXT    REFERENCES owner (id) ON DELETE RESTRICT,

    qty            INTEGER NOT NULL CHECK (qty > 0),
    unit_price_idr INTEGER NOT NULL CHECK (unit_price_idr >= 0),
    subtotal_idr   INTEGER NOT NULL CHECK (subtotal_idr >= 0),
    ppn_idr        INTEGER NOT NULL DEFAULT 0 CHECK (ppn_idr >= 0),
    gross_idr      INTEGER NOT NULL CHECK (gross_idr >= 0),

    -- The cost basis decision for this line, snapshotted (SPEC §3.2). Equal to
    -- gross_idr unless the buying entity is PKP and holds the faktur, in which
    -- case the PPN was recoverable and is not cost. Stored rather than
    -- rederived so the layer's cost can always be explained from the document
    -- that produced it.
    cost_total_idr INTEGER NOT NULL CHECK (cost_total_idr >= 0),
    -- The input PPN this line contributes to the PPN position (SPEC §2.4).
    -- Zero unless PKP-with-faktur. Kept beside cost_total_idr because together
    -- they must account for every rupiah of gross_idr, and the check below says
    -- so out loud.
    creditable_ppn_idr INTEGER NOT NULL DEFAULT 0 CHECK (creditable_ppn_idr >= 0),

    -- The layer this line created. One line, one layer.
    stock_layer_id TEXT    NOT NULL REFERENCES stock_layer (id) ON DELETE RESTRICT,
    expiry_date    TEXT    CHECK (expiry_date IS NULL
                                  OR expiry_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at     INTEGER NOT NULL,

    CHECK (gross_idr = subtotal_idr + ppn_idr),
    -- Every rupiah paid is either cost or recoverable credit. Never both,
    -- never lost. This is SPEC §3.2 as an arithmetic identity.
    CHECK (gross_idr = cost_total_idr + creditable_ppn_idr)
) STRICT;

CREATE INDEX idx_purchase_line_purchase ON purchase_line (purchase_id);
CREATE INDEX idx_purchase_line_product ON purchase_line (product_id);
CREATE UNIQUE INDEX idx_purchase_line_layer ON purchase_line (stock_layer_id);

-- Purchase return. R12.2: removes stock, reverses the cost layer and any input
-- PPN claimed.
--
-- Goods go back to the supplier, so stock leaves: the return appends a
-- stock_consumption with a positive qty_out against the original layer. It is
-- not a sales return and does not share its shape (D-010) -- that one puts
-- stock back and carries a negative qty_out.
CREATE TABLE purchase_return (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id     TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    purchase_id   TEXT    NOT NULL REFERENCES purchase (id) ON DELETE RESTRICT,
    occurred_at   INTEGER NOT NULL,
    business_date TEXT    NOT NULL CHECK (business_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- Required. A correction nobody can explain later is what the audit log
    -- exists to prevent (R12.5, R7.2).
    reason        TEXT    NOT NULL CHECK (length(trim(reason)) > 0),
    cost_idr      INTEGER NOT NULL CHECK (cost_idr >= 0),
    -- Input PPN given back. Only ever non-zero where it was claimed in the
    -- first place, which is why it is tracked separately from cost: claiming
    -- credit on goods that went back is the error R12.2 is guarding against.
    ppn_reversed_idr INTEGER NOT NULL DEFAULT 0 CHECK (ppn_reversed_idr >= 0),
    created_by    TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_purchase_return_purchase ON purchase_return (purchase_id);
CREATE INDEX idx_purchase_return_entity_date ON purchase_return (entity_id, business_date);

CREATE TABLE purchase_return_line (
    id                 TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    purchase_return_id TEXT    NOT NULL REFERENCES purchase_return (id) ON DELETE RESTRICT,
    purchase_line_id   TEXT    NOT NULL REFERENCES purchase_line (id) ON DELETE RESTRICT,
    stock_layer_id     TEXT    NOT NULL REFERENCES stock_layer (id) ON DELETE RESTRICT,
    -- The consumption row that actually took the stock back out. Named so the
    -- movement and the document that ordered it cannot drift apart.
    consumption_id     TEXT    NOT NULL REFERENCES stock_consumption (id) ON DELETE RESTRICT,
    qty                INTEGER NOT NULL CHECK (qty > 0),
    cost_idr           INTEGER NOT NULL CHECK (cost_idr >= 0),
    ppn_reversed_idr   INTEGER NOT NULL DEFAULT 0 CHECK (ppn_reversed_idr >= 0),
    created_at         INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_purchase_return_line_return ON purchase_return_line (purchase_return_id);
CREATE INDEX idx_purchase_return_line_line ON purchase_return_line (purchase_line_id);

-- INV-2: a finalised transaction is immutable. Corrections are compensating
-- records -- for a purchase, that is a purchase return.

-- +goose StatementBegin
CREATE TRIGGER purchase_is_immutable_update
BEFORE UPDATE ON purchase
BEGIN
    SELECT RAISE(ABORT, 'purchase is immutable (INV-2): correct it with a purchase return (R12.2), never an UPDATE');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER purchase_is_immutable_delete
BEFORE DELETE ON purchase
BEGIN
    SELECT RAISE(ABORT, 'purchase is immutable (INV-2): correct it with a purchase return (R12.2), never a DELETE');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER purchase_line_is_immutable_update
BEFORE UPDATE ON purchase_line
BEGIN
    SELECT RAISE(ABORT, 'purchase_line is immutable (INV-2): it justifies a stock layer cost that has already been settled on');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER purchase_line_is_immutable_delete
BEFORE DELETE ON purchase_line
BEGIN
    SELECT RAISE(ABORT, 'purchase_line is immutable (INV-2): it justifies a stock layer cost that has already been settled on');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER purchase_return_is_immutable_update
BEFORE UPDATE ON purchase_return
BEGIN
    SELECT RAISE(ABORT, 'purchase_return is immutable (INV-2)');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER purchase_return_is_immutable_delete
BEFORE DELETE ON purchase_return
BEGIN
    SELECT RAISE(ABORT, 'purchase_return is immutable (INV-2)');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER purchase_return_is_immutable_delete;
DROP TRIGGER purchase_return_is_immutable_update;
DROP TRIGGER purchase_line_is_immutable_delete;
DROP TRIGGER purchase_line_is_immutable_update;
DROP TRIGGER purchase_is_immutable_delete;
DROP TRIGGER purchase_is_immutable_update;
DROP INDEX idx_purchase_return_line_line;
DROP INDEX idx_purchase_return_line_return;
DROP TABLE purchase_return_line;
DROP INDEX idx_purchase_return_entity_date;
DROP INDEX idx_purchase_return_purchase;
DROP TABLE purchase_return;
DROP INDEX idx_purchase_line_layer;
DROP INDEX idx_purchase_line_product;
DROP INDEX idx_purchase_line_purchase;
DROP TABLE purchase_line;
DROP INDEX idx_purchase_invoice;
DROP INDEX idx_purchase_faktur;
DROP INDEX idx_purchase_supplier;
DROP INDEX idx_purchase_entity_date;
DROP TABLE purchase;
