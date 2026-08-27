-- The owner-margin reporting rule. TASKS 3.4, SPEC 4.4, DECISIONS D-012.
--
-- One question stood between the margin report and being finished, and it was
-- not a technical one: a November return of an October sale, when October's
-- money has already been split between family members.
--
--   RETURN_DATE  the reversal comes out of November. October stays exactly as
--                it was settled, so nobody is asked to hand money back. The
--                cost is that October's report overstates what really sold.
--   SALE_DATE    October is restated so the month reads correctly, and the
--                settlement it produced is now out of date. Nothing stops that
--                -- the user declined period locking (R7.3) -- so it shows up
--                only in the audit log, after the money moved.
--
-- Resolved as RETURN_DATE (D-012): it never restates money that has already
-- changed hands, it puts the margin in the same month as the refund that left
-- the till, and it is the recoverable direction if the family later decides
-- otherwise. The reasoning is in DECISIONS; the alternative stays implemented
-- and switchable, because which one is right depends on how this family
-- actually settles and that can change.
--
-- No row means the default. A row means somebody chose, and updated_by says who
-- -- which is the whole distinction worth storing, so there is no separate
-- "confirmed" flag.
--
-- Deliberately NOT effective-dated, unlike tax_rule (INV-4). A tax rate is a
-- fact about a date -- the rate on a 2025 invoice does not change when the law
-- does, so history is snapshotted (INV-3). This is a rule for reading history,
-- not a property of any transaction in it: changing it changes what every past
-- report says, which is exactly why the change is audited and why the decision
-- wanted making once. If the family ever needs a mid-year switch, that is a
-- conversation about re-settlement, not a schema change made in advance.

-- +goose Up

CREATE TABLE margin_setting (
    entity_id          TEXT    NOT NULL PRIMARY KEY REFERENCES legal_entity (id) ON DELETE RESTRICT,

    -- Which period a return counts in. SPEC 4.4, D-012.
    return_period_rule TEXT    NOT NULL DEFAULT 'RETURN_DATE'
                               CHECK (return_period_rule IN ('RETURN_DATE', 'SALE_DATE')),

    -- Free text, so the reason survives the person who decided it. Six months
    -- on, "why does a retur land in the month it came back" is a question with
    -- a real answer and nowhere else to keep it.
    note               TEXT,

    updated_by         TEXT    REFERENCES app_user (id) ON DELETE SET NULL,
    updated_at         INTEGER NOT NULL,
    created_at         INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE margin_setting;
