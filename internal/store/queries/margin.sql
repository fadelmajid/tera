-- Laporan Margin per Owner. TASKS 3.1-3.6, SPEC 4.
-- Keep this file ASCII-only -- see README.
--
-- Six range-scoped reads, not a query per sale. The whole period is loaded once
-- and internal/domain/margin does the arithmetic, because the arithmetic is the
-- part that has to be tested exhaustively and SQL is the worst place to test it.
--
-- Nothing here aggregates money. Every rupiah figure the report shows is summed
-- in Go from the rows below, so the total and the drill-down are the same
-- numbers added up twice rather than two queries that have to agree (SPEC 4.2).

-- name: GetMarginSetting :one
SELECT * FROM margin_setting WHERE entity_id = sqlc.arg(entity_id);

-- A row here means a person chose; no row means the default (D-012). The rule
-- is written whole rather than patched, so one row is one decision.
-- name: UpsertMarginSetting :one
INSERT INTO margin_setting (
    entity_id, return_period_rule, note, updated_by, updated_at, created_at
) VALUES (
    sqlc.arg(entity_id), sqlc.arg(return_period_rule),
    sqlc.narg(note), sqlc.narg(updated_by), sqlc.arg(updated_at), sqlc.arg(created_at)
)
ON CONFLICT (entity_id) DO UPDATE SET
    return_period_rule = excluded.return_period_rule,
    note               = excluded.note,
    updated_by         = excluded.updated_by,
    updated_at         = excluded.updated_at
RETURNING *;

-- Finalised sales only. A void says the sale did not happen (R12.3): its
-- reversals share the sale's movement id, so a voided sale would net to nothing
-- and still put a phantom row in the drill-down.
-- name: ListSalesForMargin :many
SELECT
    s.id            AS id,
    s.invoice_no    AS invoice_no,
    s.business_date AS business_date,
    s.occurred_at   AS occurred_at,
    s.cogs_idr      AS cogs_idr,
    c.name          AS customer_name
FROM sale s
LEFT JOIN customer c ON c.id = s.customer_id
WHERE s.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND s.business_date >= sqlc.arg(from_date)
  AND s.business_date <= sqlc.arg(to_date)
ORDER BY s.business_date, s.invoice_no, s.id;

-- Revenue, and the owner it was attributed to at the moment of sale. owner_id
-- is read from the line, not from the product: re-tagging a product later must
-- not move money that has already been settled (INV-8).
--
-- Revenue is dpp_idr, not net_idr. COGS comes off a stock layer already net of
-- creditable PPN (SPEC 3.2), so revenue has to be net of PPN as well or the
-- subtraction is not comparing like with like. Under inclusive pricing net_idr
-- contains the tax, and reading it here would overstate every owner's margin by
-- the PPN rate -- in the report the family settles money on. net_idr is still
-- selected: it is what the customer paid, and the drill-down shows both.
-- name: ListSaleLinesForMargin :many
SELECT
    l.id                AS id,
    l.sale_id           AS sale_id,
    l.product_id        AS product_id,
    l.owner_id          AS owner_id,
    l.qty               AS qty,
    l.net_idr           AS net_idr,
    l.dpp_idr           AS dpp_idr,
    l.ppn_idr           AS ppn_idr,
    l.cogs_idr          AS cogs_idr,
    p.code              AS product_code,
    p.name              AS product_name
FROM sale_line l
JOIN sale s    ON s.id = l.sale_id
JOIN product p ON p.id = l.product_id
WHERE s.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND s.business_date >= sqlc.arg(from_date)
  AND s.business_date <= sqlc.arg(to_date)
ORDER BY l.sale_id, l.created_at, l.id;

-- COGS, from the actual layers drawn (SPEC 4.1), and the bottom of the
-- drill-down (SPEC 4.2). owner_id comes from the layer here -- recorded when
-- the goods were bought -- which is the second, independent attribution the
-- domain checks against the line above.
-- name: ListSaleDrawsForMargin :many
SELECT
    c.id              AS id,
    c.movement_id     AS sale_id,
    c.layer_id        AS layer_id,
    c.qty_out         AS qty_out,
    c.cost_idr        AS cost_idr,
    c.reverses_id     AS reverses_id,
    lay.product_id      AS product_id,
    lay.owner_id        AS owner_id,
    lay.acquired_at     AS layer_acquired_at,
    lay.qty_in          AS layer_qty_in,
    lay.cost_total_idr  AS layer_cost_total_idr,
    lay.source          AS layer_source,
    lay.faktur_received AS layer_faktur_received
FROM stock_consumption c
JOIN stock_layer lay ON lay.id = c.layer_id
JOIN sale s          ON s.id = c.movement_id
WHERE s.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND s.business_date >= sqlc.arg(from_date)
  AND s.business_date <= sqlc.arg(to_date)
  AND c.movement_type = 'SALE'
ORDER BY c.movement_id, lay.acquired_at, lay.id, c.created_at;

-- Returns touching the window from EITHER side.
--
-- Deliberately not filtered by the rule in force. Whether a return counts in
-- the month it came back or the month it was sold is SPEC 4.4's open question,
-- and that question is answered in exactly one place -- internal/domain/margin
-- -- where it is tested. Encoding it here as well would make two implementations
-- of the same rule, and the day they disagree is a settlement nobody can
-- reconcile. At under 1,000 transactions a day, loading the handful of extra
-- rows costs nothing.
-- name: ListReturnsForMargin :many
SELECT
    r.id                 AS id,
    r.sale_id            AS sale_id,
    r.business_date      AS business_date,
    r.sale_business_date AS sale_business_date,
    r.occurred_at        AS occurred_at,
    r.reason             AS reason,
    r.refund_idr         AS refund_idr,
    r.cogs_reversed_idr  AS cogs_reversed_idr,
    s.invoice_no         AS sale_invoice_no
FROM sale_return r
JOIN sale s ON s.id = r.sale_id
WHERE r.entity_id = sqlc.arg(entity_id)
  -- A void says the sale did not happen, and voiding gives back whatever the
  -- return had not already returned. Leaving the return on the report would
  -- reduce an owner's margin against revenue that is no longer there.
  AND s.status = 'FINAL'
  AND ((r.business_date      >= sqlc.arg(from_date) AND r.business_date      <= sqlc.arg(to_date))
    OR (r.sale_business_date >= sqlc.arg(from_date) AND r.sale_business_date <= sqlc.arg(to_date)))
ORDER BY r.business_date, r.id;

-- The owner comes from the sale line the goods went out on, so a return lands
-- in the same person's bucket the revenue did (D-010).
-- name: ListReturnLinesForMargin :many
SELECT
    rl.id                AS id,
    rl.sale_return_id    AS sale_return_id,
    rl.sale_line_id      AS sale_line_id,
    rl.qty               AS qty,
    rl.refund_idr        AS refund_idr,
    -- The PPN inside the refund. Revenue reversed is refund_idr less this:
    -- the tax handed back was never the owner's margin to begin with, and it
    -- nets against output PPN in the position report instead (SPEC 2.4). Zero
    -- on returns recorded before the tax engine landed, which is correct --
    -- those sales carried none.
    rl.ppn_reversed_idr  AS ppn_reversed_idr,
    rl.cogs_reversed_idr AS cogs_reversed_idr,
    sl.product_id        AS product_id,
    sl.owner_id          AS owner_id,
    p.code               AS product_code,
    p.name               AS product_name
FROM sale_return_line rl
JOIN sale_return r ON r.id = rl.sale_return_id
JOIN sale s        ON s.id = r.sale_id
JOIN sale_line sl  ON sl.id = rl.sale_line_id
JOIN product p     ON p.id = sl.product_id
WHERE r.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND ((r.business_date      >= sqlc.arg(from_date) AND r.business_date      <= sqlc.arg(to_date))
    OR (r.sale_business_date >= sqlc.arg(from_date) AND r.sale_business_date <= sqlc.arg(to_date)))
ORDER BY rl.sale_return_id, rl.created_at, rl.id;

-- The reversal rows: negative quantity, negative cost, each naming the draw it
-- gives back (D-010). They restore stock to the layer it came from, which is
-- what makes the margin reverse at exactly the cost that was taken.
-- name: ListReturnDrawsForMargin :many
SELECT
    c.id              AS id,
    c.movement_id     AS sale_return_id,
    c.layer_id        AS layer_id,
    c.qty_out         AS qty_out,
    c.cost_idr        AS cost_idr,
    c.reverses_id     AS reverses_id,
    lay.product_id      AS product_id,
    lay.owner_id        AS owner_id,
    lay.acquired_at     AS layer_acquired_at,
    lay.qty_in          AS layer_qty_in,
    lay.cost_total_idr  AS layer_cost_total_idr,
    lay.source          AS layer_source,
    lay.faktur_received AS layer_faktur_received
FROM stock_consumption c
JOIN stock_layer lay ON lay.id = c.layer_id
JOIN sale_return r   ON r.id = c.movement_id
JOIN sale s          ON s.id = r.sale_id
WHERE r.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND ((r.business_date      >= sqlc.arg(from_date) AND r.business_date      <= sqlc.arg(to_date))
    OR (r.sale_business_date >= sqlc.arg(from_date) AND r.sale_business_date <= sqlc.arg(to_date)))
ORDER BY c.movement_id, lay.acquired_at, lay.id, c.created_at;
