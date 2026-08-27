-- The omzet clock: turnover against the Rp 4,8 miliar PKP threshold.
-- TASKS 7.1, SPEC 5. INV-4, INV-5.
--
-- Migration numbering: SPEC 5.1 and TASKS 7.1 said "migration 008", written
-- before the earlier phases took that number. The omzet tables are 014.
--
-- The rule this schema exists to get right: the threshold is measured PER BOOK
-- YEAR, cumulative, reset annually (PMK 197/2013). Common guidance says
-- "rolling 12 months" and is wrong. Both figures are produced -- the book-year
-- one is the law and the trailing one is a pace estimate -- and they are
-- computed over different windows in internal/domain/omzet, never conflated.
--
-- Decisions taken here:
--
--   The ledger is append-only, like the FIFO layers. A crossing is a legal
--   event with a date attached, and a ledger that can be edited cannot answer
--   when it happened. Corrections are rows.
--
--   effective_date is not the day the row was written. A void in January of a
--   December sale carries December's date, because the turnover has to be
--   removed from the year it was counted in -- and the threshold resets between
--   those two years (SPEC 5.4). A sales return carries the day the goods came
--   back, because the turnover did happen and is being reduced now: that is the
--   same distinction D-012 draws for margin, and it is why a void and a return
--   are separate event types rather than one word for both.
--
--   book_year is denormalised at write time, in the entity's zone (D-005,
--   INV-5), and cross-checked against the calendar on every read. A row whose
--   stored year disagrees with its date means something wrote it in the wrong
--   zone, and every figure built on it is measured against the wrong window.
--
--   The threshold is a config row, not a constant (INV-4), effective-dated with
--   its legal_ref like a tax rule. So is how the two deadline dates are derived
--   from a crossing -- because which regulation governs them is genuinely not
--   settled. See testdata/worked_examples/omzet_unverified.json.

-- +goose Up

CREATE TABLE omzet_ledger (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id      TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,

    -- Denormalised from effective_date in the entity's timezone, honouring its
    -- book_year_start_month. Labelled by the year the book year opens in: a
    -- book year running April 2026 to March 2027 is 2026.
    book_year      INTEGER NOT NULL,
    -- The day this counts on, which is not always the day it was written.
    effective_date TEXT    NOT NULL
                           CHECK (effective_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),

    event_type     TEXT    NOT NULL CHECK (event_type IN
                           ('SALE', 'VOID', 'REFUND', 'RETURN', 'ADJUSTMENT')),

    -- Signed: positive for turnover, negative for a reversal. Whole rupiah
    -- (INV-1). Zero is refused -- a row that moves nothing is a row that means
    -- nothing, and it would only ever be a bug upstream.
    signed_amount_idr INTEGER NOT NULL CHECK (signed_amount_idr <> 0),

    -- The sale, void or return behind the row, so every figure decomposes into
    -- the documents that produced it.
    source_txn_id  TEXT,
    note           TEXT,
    created_by     TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,

    -- Turnover is positive and reversals are negative. A sale that reduces
    -- turnover or a void that increases it is a sign flip upstream, and it
    -- would move the crossing date.
    CHECK ((event_type = 'SALE' AND signed_amount_idr > 0)
        OR (event_type IN ('VOID', 'REFUND', 'RETURN') AND signed_amount_idr < 0)
        OR  event_type = 'ADJUSTMENT')
) STRICT;

-- The read the clock makes: one company, one book year, in date order.
CREATE INDEX idx_omzet_ledger_year ON omzet_ledger (entity_id, book_year, effective_date);
-- And the trailing-twelve-month estimate, which crosses book years.
CREATE INDEX idx_omzet_ledger_date ON omzet_ledger (entity_id, effective_date);
CREATE INDEX idx_omzet_ledger_source ON omzet_ledger (source_txn_id);

-- Append-only. A crossing is a legal event with a date attached.

-- +goose StatementBegin
CREATE TRIGGER omzet_ledger_is_immutable_update
BEFORE UPDATE ON omzet_ledger
BEGIN
    SELECT RAISE(ABORT, 'omzet_ledger is append-only (SPEC 5.1): correct it with another row');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER omzet_ledger_no_delete
BEFORE DELETE ON omzet_ledger
BEGIN
    SELECT RAISE(ABORT, 'omzet_ledger is append-only (SPEC 5.1): turnover that happened cannot stop having happened');
END;
-- +goose StatementEnd

-- The threshold, effective-dated (INV-4).
CREATE TABLE omzet_threshold (
    id             TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    entity_id      TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,

    -- Rp 4.800.000.000 since PMK 197/2013. A row, never a constant: unlike a
    -- tax rate, changing this alters no historical transaction -- it changes
    -- which year a business crossed in.
    amount_idr     INTEGER NOT NULL CHECK (amount_idr > 0),

    -- Where the alarm changes colour, in basis points of the threshold: 7000
    -- is 70%. Integers, because a ratio here is not a float either (INV-1).
    watch_bp       INTEGER NOT NULL DEFAULT 7000 CHECK (watch_bp BETWEEN 0 AND 10000),
    warn_bp        INTEGER NOT NULL DEFAULT 9000 CHECK (warn_bp BETWEEN 0 AND 10000),

    -- How the two dates a crossing emits are derived. Two readings each, both
    -- implemented in domain/omzet, because which regulation governs PKP
    -- registration is not settled -- SPEC 5.2 cites a final-PPh regulation for
    -- a PPN deadline. Answering the question is a row, not a release.
    register_by_policy TEXT NOT NULL DEFAULT 'END_OF_BOOK_YEAR'
                            CHECK (register_by_policy IN
                                   ('END_OF_BOOK_YEAR', 'END_OF_FOLLOWING_MONTH')),
    vat_starts_policy  TEXT NOT NULL DEFAULT 'NEXT_BOOK_YEAR_FIRST_PERIOD'
                            CHECK (vat_starts_policy IN
                                   ('NEXT_BOOK_YEAR_FIRST_PERIOD', 'MONTH_AFTER_REGISTRATION')),

    valid_from     TEXT    NOT NULL
                           CHECK (valid_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    valid_to       TEXT    CHECK (valid_to IS NULL
                                  OR (valid_to GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
                                      AND valid_to >= valid_from)),

    -- The regulation. Shown on screen so the owner's konsultan pajak can check
    -- the figure without reading code.
    legal_ref      TEXT    NOT NULL CHECK (length(trim(legal_ref)) > 0),
    note           TEXT,
    created_by     TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL,
    closed_by      TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    closed_at      INTEGER,

    CHECK (watch_bp <= warn_bp)
) STRICT;

CREATE INDEX idx_omzet_threshold_lookup ON omzet_threshold (entity_id, valid_from);
-- At most one open-ended threshold per company: the half-done change, caught as
-- a constraint rather than as two figures both claiming today.
CREATE UNIQUE INDEX idx_omzet_threshold_open ON omzet_threshold (entity_id)
    WHERE valid_to IS NULL;

-- +goose StatementBegin
CREATE TRIGGER omzet_threshold_no_overlap
BEFORE INSERT ON omzet_threshold
WHEN EXISTS (
    SELECT 1 FROM omzet_threshold t
    WHERE t.entity_id = NEW.entity_id
      AND (t.valid_to IS NULL   OR t.valid_to   >= NEW.valid_from)
      AND (NEW.valid_to IS NULL OR NEW.valid_to >= t.valid_from)
)
BEGIN
    SELECT RAISE(ABORT, 'omzet_threshold overlaps one already in force (INV-4): close the earlier row''s valid_to first');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER omzet_threshold_only_closing_is_an_update
BEFORE UPDATE ON omzet_threshold
WHEN OLD.valid_to IS NOT NULL
  OR NEW.entity_id          <> OLD.entity_id
  OR NEW.amount_idr         <> OLD.amount_idr
  OR NEW.watch_bp           <> OLD.watch_bp
  OR NEW.warn_bp            <> OLD.warn_bp
  OR NEW.register_by_policy <> OLD.register_by_policy
  OR NEW.vat_starts_policy  <> OLD.vat_starts_policy
  OR NEW.valid_from         <> OLD.valid_from
  OR NEW.legal_ref          <> OLD.legal_ref
BEGIN
    SELECT RAISE(ABORT, 'never UPDATE a threshold (INV-4): insert a new row and close this one''s valid_to');
END;
-- +goose StatementEnd

-- Per-company settings for the clock. One row is one decision, mirroring
-- margin_setting (D-012): no row means the default.
CREATE TABLE omzet_setting (
    entity_id  TEXT    NOT NULL PRIMARY KEY REFERENCES legal_entity (id) ON DELETE RESTRICT,
    -- Whether turnover is counted before or after PPN (SPEC 5.4). Net is the
    -- default and is the only reading that means anything at the non-PKP
    -- entity, which charges no PPN and is the one that can still cross.
    base       TEXT    NOT NULL DEFAULT 'NET_OF_VAT' CHECK (base IN ('NET_OF_VAT', 'GROSS')),
    note       TEXT,
    updated_by TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE omzet_setting;
DROP TRIGGER omzet_threshold_only_closing_is_an_update;
DROP TRIGGER omzet_threshold_no_overlap;
DROP INDEX idx_omzet_threshold_open;
DROP INDEX idx_omzet_threshold_lookup;
DROP TABLE omzet_threshold;
DROP TRIGGER omzet_ledger_no_delete;
DROP TRIGGER omzet_ledger_is_immutable_update;
DROP INDEX idx_omzet_ledger_source;
DROP INDEX idx_omzet_ledger_date;
DROP INDEX idx_omzet_ledger_year;
DROP TABLE omzet_ledger;
