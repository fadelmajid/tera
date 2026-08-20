-- Master data: the entities, people, and things every later table points at.
-- R11. See docs/DECISIONS.md D-003 (ids), D-005 (time), D-006 (product scope).
--
-- Every table is STRICT. SQLite's default type affinity would happily store
-- 1000.5 in an INTEGER rupiah column; STRICT rejects it at the storage layer,
-- which makes INV-1 enforced rather than merely intended.

-- +goose Up

-- A legal entity is one of the two companies. R1.2: each is an independent
-- legal and tax entity with its own configuration. is_pkp drives materially
-- different behaviour on every sale and purchase (SPEC §2.3).
CREATE TABLE legal_entity (
    id                    TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    code                  TEXT    NOT NULL UNIQUE,
    name                  TEXT    NOT NULL,
    is_pkp                INTEGER NOT NULL CHECK (is_pkp IN (0, 1)),
    npwp                  TEXT,
    address               TEXT,
    phone                 TEXT,
    -- Book-year and business-day boundaries resolve here, never in UTC and
    -- never in the server's zone (INV-5, D-005). IANA name, e.g. Asia/Jakarta.
    timezone              TEXT    NOT NULL DEFAULT 'Asia/Jakarta',
    -- The book year need not start in January (SPEC §5.4).
    book_year_start_month INTEGER NOT NULL DEFAULT 1
                                  CHECK (book_year_start_month BETWEEN 1 AND 12),
    is_active             INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL
) STRICT;

-- An owner is a family member products are attributed to. An attribution tag,
-- not legal ownership (CLAUDE.md, R2). Deliberately NOT scoped to an entity:
-- the same people own product lines across both companies, and a transfer
-- carries owner attribution across the entity boundary unchanged (SPEC §3.4).
CREATE TABLE owner (
    id         TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    code       TEXT    NOT NULL UNIQUE,
    name       TEXT    NOT NULL,
    note       TEXT,
    is_active  INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- The product catalogue is shared across both entities (D-006). Stock is what
-- is entity-scoped, on stock_layer, not the identity of the thing itself.
CREATE TABLE product (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    code           TEXT    NOT NULL UNIQUE,
    barcode        TEXT,
    name           TEXT    NOT NULL,
    unit           TEXT    NOT NULL DEFAULT 'pcs',
    -- category is for categories. The user currently bends this field into an
    -- ownership dimension because there is no proper slot for it
    -- (REQUIREMENTS §2); owner_id is that slot, and this field goes back to
    -- meaning what it says.
    category       TEXT,
    -- NULL is the company bucket, reported alongside the named owners (R2.2).
    -- Nullable by requirement, not by oversight.
    owner_id       TEXT    REFERENCES owner (id) ON DELETE RESTRICT,
    -- Rupiah. INTEGER in a STRICT table, so a float cannot be stored (INV-1).
    sale_price_idr INTEGER NOT NULL DEFAULT 0 CHECK (sale_price_idr >= 0),
    is_active      INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_product_owner ON product (owner_id);
CREATE INDEX idx_product_category ON product (category);
-- Barcodes are scanned to identify one product (R9.3), so a duplicate is a
-- bug. Partial index: many products legitimately have no barcode.
CREATE UNIQUE INDEX idx_product_barcode ON product (barcode) WHERE barcode IS NOT NULL;

-- R11.2. Suppliers are shared across entities; which entity bought is recorded
-- on the purchase (R10.5), because that is what decides whether input PPN is
-- creditable at all.
CREATE TABLE supplier (
    id            TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    code          TEXT    NOT NULL UNIQUE,
    name          TEXT    NOT NULL,
    npwp          TEXT,
    address       TEXT,
    phone         TEXT,
    -- Whether this supplier NORMALLY issues a faktur. An expectation for the
    -- purchasing screen to default from and for supplier comparison (R10.6) —
    -- never the truth for a given purchase. That truth is faktur_received on
    -- the stock layer, and it is what sets the layer's cost basis (INV-9).
    issues_faktur INTEGER NOT NULL DEFAULT 0 CHECK (issues_faktur IN (0, 1)),
    is_active     INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;

-- R11.3. npwp for businesses, nik for individuals — a buyer who wants a faktur
-- must supply one of them.
CREATE TABLE customer (
    id         TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    code       TEXT    NOT NULL UNIQUE,
    name       TEXT    NOT NULL,
    npwp       TEXT,
    nik        TEXT,
    address    TEXT,
    phone      TEXT,
    is_active  INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE customer;
DROP TABLE supplier;
DROP INDEX idx_product_barcode;
DROP INDEX idx_product_category;
DROP INDEX idx_product_owner;
DROP TABLE product;
DROP TABLE owner;
DROP TABLE legal_entity;
