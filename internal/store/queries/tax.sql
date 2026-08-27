-- Tax rules, the sale-time snapshot, and the PPN position.
-- TASKS 5.1, 5.8, 5.9, 5.10. SPEC 2.1, 2.3, 2.4.
-- Keep this file ASCII-only -- see README.
--
-- Nothing here computes a rate. These queries read config rows and write
-- snapshots; the arithmetic is internal/domain/tax, which is where it can be
-- tested against worked examples (INV-4).
--
-- A rate is never UPDATEd. CloseTaxRule sets valid_to and nothing else, and the
-- trigger in migration 012 refuses any other edit.

-- name: ListTaxRules :many
SELECT * FROM tax_rule
WHERE entity_id = sqlc.arg(entity_id)
ORDER BY tax_type, valid_from DESC, id;

-- name: GetTaxRule :one
SELECT * FROM tax_rule WHERE id = sqlc.arg(id);

-- Every rule the entity has ever had, for the engine to select from by business
-- date. Loaded whole rather than filtered in SQL: the effective-dated selection
-- and its overlap check live in domain/tax, where they are tested, and at two
-- rules per tax type there is nothing to optimise.
-- name: ListTaxRulesForEntity :many
SELECT * FROM tax_rule
WHERE entity_id = sqlc.arg(entity_id) AND tax_type = sqlc.arg(tax_type)
ORDER BY valid_from, id;

-- name: CreateTaxRule :one
INSERT INTO tax_rule (
    id, entity_id, tax_type, rate_bp, dpp_factor_num, dpp_factor_den,
    is_inclusive, calculation_level, rounding_mode, rounding_unit,
    valid_from, valid_to, legal_ref, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(tax_type), sqlc.arg(rate_bp),
    sqlc.arg(dpp_factor_num), sqlc.arg(dpp_factor_den), sqlc.arg(is_inclusive),
    sqlc.arg(calculation_level), sqlc.arg(rounding_mode), sqlc.arg(rounding_unit),
    sqlc.arg(valid_from), sqlc.narg(valid_to), sqlc.arg(legal_ref), sqlc.narg(note),
    sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- The only permitted edit (INV-4): close the window so a new row can open the
-- next day. The WHERE clause makes it a no-op on an already-closed rule rather
-- than a trigger abort.
-- name: CloseTaxRule :one
UPDATE tax_rule
SET valid_to = sqlc.arg(valid_to),
    closed_by = sqlc.narg(closed_by),
    closed_at = sqlc.arg(closed_at)
WHERE id = sqlc.arg(id) AND valid_to IS NULL
RETURNING *;

-- Only a rule that has not started yet may be removed, and the service checks
-- that against the entity's own clock before calling this. A rule already in
-- force is closed, never deleted: sales were priced under it and the report
-- that explains them reads its citation.
-- name: DeleteTaxRule :exec
DELETE FROM tax_rule WHERE id = sqlc.arg(id) AND valid_from > sqlc.arg(today);

-- --- the sale-time snapshot (INV-3) -----------------------------------------

-- name: CreateSaleTax :one
INSERT INTO sale_tax (
    id, sale_id, sale_line_id, tax_type, rate_bp, dpp_factor_num, dpp_factor_den,
    is_inclusive, calculation_level, rounding_mode, rounding_unit, legal_ref,
    tax_rule_id, is_exempt, dpp_idr, ppn_idr, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(sale_id), sqlc.arg(sale_line_id), sqlc.arg(tax_type),
    sqlc.arg(rate_bp), sqlc.arg(dpp_factor_num), sqlc.arg(dpp_factor_den),
    sqlc.arg(is_inclusive), sqlc.arg(calculation_level), sqlc.arg(rounding_mode),
    sqlc.arg(rounding_unit), sqlc.arg(legal_ref), sqlc.narg(tax_rule_id),
    sqlc.arg(is_exempt), sqlc.arg(dpp_idr), sqlc.arg(ppn_idr), sqlc.arg(created_at)
)
RETURNING *;

-- The tax breakdown of one sale, for the receipt and for anyone asking why a
-- figure is what it is. Reads the snapshot, never the current rule.
-- name: ListSaleTax :many
SELECT
    t.*,
    l.product_id AS product_id,
    l.qty        AS qty,
    l.net_idr    AS net_idr,
    p.name       AS product_name
FROM sale_tax t
JOIN sale_line l ON l.id = t.sale_line_id
JOIN product p ON p.id = l.product_id
WHERE t.sale_id = sqlc.arg(sale_id)
ORDER BY l.created_at, l.id;

-- --- the PPN position (SPEC 2.4) --------------------------------------------

-- Output PPN for a masa pajak, split by whether a faktur was issued.
--
-- The split is TASKS 5.4 made visible. A PKP owes output PPN on the delivery of
-- taxable goods whether or not the buyer took a faktur, so the without-faktur
-- column is a real liability with no document anywhere to remind anyone it
-- exists. Summed into one figure it disappears; on its own line it is the
-- number a newly registered business is surprised by.
--
-- Voided sales are excluded: a void says the sale did not happen.
-- name: SumOutputPPN :one
SELECT
    CAST(COALESCE(SUM(CASE WHEN faktur_issued = 1 THEN ppn_idr ELSE 0 END), 0) AS INTEGER)
        AS with_faktur_idr,
    CAST(COALESCE(SUM(CASE WHEN faktur_issued = 0 THEN ppn_idr ELSE 0 END), 0) AS INTEGER)
        AS without_faktur_idr,
    CAST(COALESCE(SUM(dpp_idr), 0) AS INTEGER) AS dpp_idr
FROM sale
WHERE entity_id = sqlc.arg(entity_id)
  AND status = 'FINAL'
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- Output PPN given back by sales returns, counted in the month the goods came
-- back. Mirrors SumReversedInputPPN on the purchase side.
-- name: SumReversedOutputPPN :one
SELECT CAST(COALESCE(SUM(ppn_reversed_idr), 0) AS INTEGER) AS ppn_idr
FROM sale_return
WHERE entity_id = sqlc.arg(entity_id)
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- PPN paid to suppliers that no faktur ever arrived for.
--
-- Reported so the figure is visible and never netted into the credit. It is
-- cost, and it is already sitting in the stock layers (SPEC 3.2) -- crediting
-- it here as well would claim the same rupiah twice, once against output PPN
-- and once as a lower COGS in the margin the family settles on.
-- name: SumNonCreditableInputPPN :one
SELECT CAST(COALESCE(SUM(l.ppn_idr), 0) AS INTEGER) AS ppn_idr
FROM purchase_line l
JOIN purchase p ON p.id = l.purchase_id
WHERE p.entity_id = sqlc.arg(entity_id)
  AND p.business_date >= sqlc.arg(from_date)
  AND p.business_date <= sqlc.arg(to_date)
  AND l.creditable_ppn_idr = 0;

-- The sales behind an output PPN figure, so every number on the position report
-- decomposes into the transactions that made it.
-- name: ListSalesForPPN :many
SELECT
    s.id            AS id,
    s.invoice_no    AS invoice_no,
    s.business_date AS business_date,
    s.faktur_issued AS faktur_issued,
    s.faktur_no     AS faktur_no,
    s.dpp_idr       AS dpp_idr,
    s.ppn_idr       AS ppn_idr,
    s.total_idr     AS total_idr,
    c.name          AS customer_name
FROM sale s
LEFT JOIN customer c ON c.id = s.customer_id
WHERE s.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND s.ppn_idr <> 0
  AND s.business_date >= sqlc.arg(from_date)
  AND s.business_date <= sqlc.arg(to_date)
ORDER BY s.business_date, s.invoice_no, s.id;

-- The purchases behind the input side, with the faktur fact that decides
-- whether each one counted (INV-9).
-- name: ListPurchasesForPPN :many
SELECT
    p.id              AS id,
    p.invoice_no      AS invoice_no,
    p.business_date   AS business_date,
    p.faktur_received AS faktur_received,
    p.faktur_no       AS faktur_no,
    p.ppn_idr         AS ppn_idr,
    p.total_idr       AS total_idr,
    CAST(COALESCE(SUM(l.creditable_ppn_idr), 0) AS INTEGER) AS creditable_ppn_idr,
    s.name            AS supplier_name
FROM purchase p
LEFT JOIN supplier s ON s.id = p.supplier_id
LEFT JOIN purchase_line l ON l.purchase_id = p.id
WHERE p.entity_id = sqlc.arg(entity_id)
  AND p.ppn_idr <> 0
  AND p.business_date >= sqlc.arg(from_date)
  AND p.business_date <= sqlc.arg(to_date)
GROUP BY p.id
ORDER BY p.business_date, p.invoice_no, p.id;
