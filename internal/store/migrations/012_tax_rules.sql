-- Effective-dated tax configuration, and what PKP status means, enforced.
-- TASKS 5.1, 5.5. SPEC 2.1, 2.3. INV-4.
--
-- Migration numbering: SPEC 2.1 said "migration 006", written before the earlier
-- phases took that number. The tax tables are migrations 012 and 013.
--
-- The rule this table exists to make mechanical: NEVER UPDATE A RATE. Insert a
-- new row and close the previous one's valid_to (INV-4). Indonesian VAT moved
-- three times in eighteen months and the last move changed the shape of the
-- rule rather than only its number -- the statutory rate went to 12% while the
-- effective rate stayed at 11%, by applying it to a DPP nilai lain of 11/12.
-- A schema that stored one rate would have needed a migration; this one needs
-- a row.
--
-- Decisions taken here:
--
--   The DPP nilai lain is two integer columns, not a decimal. 11/12 has no
--   finite decimal form, so any stored decimal is an approximation of a figure
--   the regulation states exactly (SPEC 2.1). Two columns, and the domain
--   carries the fraction whole to a single rounding at the end.
--
--   legal_ref is NOT NULL and non-empty. The owner's konsultan pajak reads this
--   column to check the system charges what the law says; a rule with no
--   citation is a number somebody typed and nobody outside this repo can
--   verify it.
--
--   An inclusive rule must round to whole rupiah. Under inclusive pricing the
--   tax is the remainder -- price minus DPP -- so rounding the DPP to a coarser
--   unit can push it past the price and make the tax negative. The property
--   test in domain/tax found that; the CHECK is here so config cannot reach
--   the state at all.
--
--   Rules are not deleted by a trigger rule. Deleting one cannot corrupt
--   history -- every sale carries its own tax snapshot (INV-3) -- but deleting
--   one already in force would silently stop the till, so the service layer
--   refuses it and writes an audit row either way (INV-10). The service knows
--   the entity's clock and today's date in it; a trigger does not.

-- +goose Up

CREATE TABLE tax_rule (
    id                TEXT    NOT NULL PRIMARY KEY CHECK (length(id) = 36 AND id = lower(id)),
    -- The two companies are taxed differently and a rule never crosses between
    -- them: one is PKP and the other legally cannot charge PPN (SPEC 2.3).
    entity_id         TEXT    NOT NULL REFERENCES legal_entity (id) ON DELETE RESTRICT,
    tax_type          TEXT    NOT NULL CHECK (tax_type IN ('PPN', 'PPNBM')),

    -- Basis points. 12% is 1200. An integer, so no rate is ever a float
    -- (INV-1). 10000 bp is 100%: above it the tax would exceed the goods.
    rate_bp           INTEGER NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000),

    -- The DPP nilai lain as an exact fraction. 11/12 for non-luxury PPN under
    -- PMK 131/2024; 1/1 where the base is the full price. A nilai lain reduces
    -- the base, so the numerator can never exceed the denominator -- a factor
    -- above 1 would tax more than the transaction.
    dpp_factor_num    INTEGER NOT NULL CHECK (dpp_factor_num > 0),
    dpp_factor_den    INTEGER NOT NULL CHECK (dpp_factor_den > 0),

    -- Whether the listed price already contains the tax. Decides which of the
    -- two formulas in SPEC 2.2 applies.
    is_inclusive      INTEGER NOT NULL DEFAULT 1 CHECK (is_inclusive IN (0, 1)),

    -- Where rounding happens. Under INVOICE the invoice figure is the one that
    -- goes on the faktur and the lines are allocated out of it; under LINE each
    -- line rounds and the invoice is their sum. Either is defensible and they
    -- differ by a rupiah or two, which is exactly why it is configuration.
    calculation_level TEXT    NOT NULL DEFAULT 'INVOICE'
                              CHECK (calculation_level IN ('LINE', 'INVOICE')),

    -- HALF_UP is the only mode domain/tax implements, and it refuses any other
    -- rather than approximating one. The CHECK holds the two in step: a mode
    -- stored here that the engine cannot honour would fail at the till.
    rounding_mode     TEXT    NOT NULL DEFAULT 'HALF_UP' CHECK (rounding_mode IN ('HALF_UP')),
    rounding_unit     INTEGER NOT NULL DEFAULT 1 CHECK (rounding_unit >= 1),

    -- Inclusive of both ends. "Close the old row's valid_to" means the old rate
    -- applied through that day and the new one starts the next; reading it as
    -- exclusive would tax the changeover day at the wrong rate, on the one day
    -- somebody is going to check.
    valid_from        TEXT    NOT NULL
                              CHECK (valid_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    valid_to          TEXT    CHECK (valid_to IS NULL
                                     OR (valid_to GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'
                                         AND valid_to >= valid_from)),

    -- e.g. 'PMK 131/2024'. Shown in the admin UI (TASKS 5.10).
    legal_ref         TEXT    NOT NULL CHECK (length(trim(legal_ref)) > 0),
    note              TEXT,

    created_by        TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    created_at        INTEGER NOT NULL,
    -- Who closed the rule and when. Closing is the only permitted edit.
    closed_by         TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    closed_at         INTEGER,

    CHECK (dpp_factor_num <= dpp_factor_den),
    CHECK (is_inclusive = 0 OR rounding_unit = 1),
    CHECK ((valid_to IS NULL AND closed_at IS NULL) OR (valid_to IS NOT NULL))
) STRICT;

-- The lookup every sale makes: the rule of this type in force for this company
-- on this business date.
CREATE INDEX idx_tax_rule_lookup ON tax_rule (entity_id, tax_type, valid_from);

-- At most one open-ended rule per company per tax. This is the half-done rate
-- change -- new row inserted, old row never closed -- caught as a constraint
-- rather than as two rates both claiming today and row order deciding which a
-- sale gets.
CREATE UNIQUE INDEX idx_tax_rule_open ON tax_rule (entity_id, tax_type)
    WHERE valid_to IS NULL;

-- The general case the partial index above cannot express: any two windows of
-- the same type that intersect at all.

-- +goose StatementBegin
CREATE TRIGGER tax_rule_no_overlap
BEFORE INSERT ON tax_rule
WHEN EXISTS (
    SELECT 1 FROM tax_rule r
    WHERE r.entity_id = NEW.entity_id
      AND r.tax_type  = NEW.tax_type
      AND (r.valid_to IS NULL   OR r.valid_to   >= NEW.valid_from)
      AND (NEW.valid_to IS NULL OR NEW.valid_to >= r.valid_from)
)
BEGIN
    SELECT RAISE(ABORT, 'tax_rule overlaps one already in force (INV-4): close the earlier rule''s valid_to before the new one''s valid_from');
END;
-- +goose StatementEnd

-- INV-4 as a storage rule rather than a discipline. Closing a rule is the one
-- permitted UPDATE; everything else about it is history the moment a sale is
-- priced under it.

-- +goose StatementBegin
CREATE TRIGGER tax_rule_only_closing_is_an_update
BEFORE UPDATE ON tax_rule
WHEN OLD.valid_to IS NOT NULL
  OR NEW.entity_id         <> OLD.entity_id
  OR NEW.tax_type          <> OLD.tax_type
  OR NEW.rate_bp           <> OLD.rate_bp
  OR NEW.dpp_factor_num    <> OLD.dpp_factor_num
  OR NEW.dpp_factor_den    <> OLD.dpp_factor_den
  OR NEW.is_inclusive      <> OLD.is_inclusive
  OR NEW.calculation_level <> OLD.calculation_level
  OR NEW.rounding_mode     <> OLD.rounding_mode
  OR NEW.rounding_unit     <> OLD.rounding_unit
  OR NEW.valid_from        <> OLD.valid_from
  OR NEW.legal_ref         <> OLD.legal_ref
  OR NEW.created_at        <> OLD.created_at
BEGIN
    SELECT RAISE(ABORT, 'never UPDATE a rate (INV-4): insert a new tax_rule row and close this one''s valid_to');
END;
-- +goose StatementEnd

-- TASKS 5.5: the non-PKP entity credits nothing, enforced rather than
-- conventional.
--
-- domain/fifo already decides this -- creditable input PPN needs a PKP buyer
-- AND the faktur (SPEC 3.2) -- and this is the same rule at the storage layer,
-- where nothing can route around it. Receiving a faktur is legitimate for a
-- non-PKP entity and is recorded; it simply credits nothing, and the PPN sits
-- in the cost of the goods instead.

-- +goose StatementBegin
CREATE TRIGGER purchase_line_non_pkp_credits_nothing
BEFORE INSERT ON purchase_line
WHEN NEW.creditable_ppn_idr <> 0
 AND (SELECT le.is_pkp
        FROM purchase p JOIN legal_entity le ON le.id = p.entity_id
       WHERE p.id = NEW.purchase_id) = 0
BEGIN
    SELECT RAISE(ABORT, 'a non-PKP entity can never credit input PPN (SPEC 2.3): the PPN paid belongs in cost_total_idr');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER purchase_line_non_pkp_credits_nothing;
DROP TRIGGER tax_rule_only_closing_is_an_update;
DROP TRIGGER tax_rule_no_overlap;
DROP INDEX idx_tax_rule_open;
DROP INDEX idx_tax_rule_lookup;
DROP TABLE tax_rule;
