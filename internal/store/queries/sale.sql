-- Sales, cash sessions, returns and voids. TASKS 2.1-2.10.
-- A sale is immutable once written (INV-2): the only UPDATE here flips it to
-- VOID, and the trigger in migration 009 refuses anything else.
-- Keep this file ASCII-only -- see README.

-- name: OpenCashSession :one
INSERT INTO cash_session (
    id, entity_id, status, opened_at, business_date, opened_by,
    opening_float_idr, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), 'OPEN', sqlc.arg(opened_at),
    sqlc.arg(business_date), sqlc.narg(opened_by), sqlc.arg(opening_float_idr),
    sqlc.arg(created_at)
)
RETURNING *;

-- name: GetCashSession :one
SELECT * FROM cash_session WHERE id = sqlc.arg(id);

-- At most one open session per company, so this is a row or nothing. It is
-- also the void window: a sale can be undone until its session closes.
-- name: GetOpenCashSession :one
SELECT * FROM cash_session
WHERE entity_id = sqlc.arg(entity_id) AND status = 'OPEN';

-- name: ListCashSessions :many
SELECT * FROM cash_session
WHERE entity_id = sqlc.arg(entity_id)
ORDER BY opened_at DESC, id DESC;

-- name: CloseCashSession :one
UPDATE cash_session
SET status = 'CLOSED',
    closed_at = sqlc.arg(closed_at),
    closed_by = sqlc.narg(closed_by),
    counted_cash_idr = sqlc.arg(counted_cash_idr),
    expected_cash_idr = sqlc.arg(expected_cash_idr),
    variance_idr = sqlc.arg(variance_idr),
    close_note = sqlc.narg(close_note)
WHERE id = sqlc.arg(id) AND status = 'OPEN'
RETURNING *;

-- The Z-report figures for one session: cash taken in, by method, plus what
-- went back out as cash refunds. Voided sales are excluded -- a void means the
-- sale did not happen, so its money was never in the drawer.
-- name: SumSessionPayments :many
SELECT
    p.method                                       AS method,
    CAST(COALESCE(SUM(p.amount_idr), 0) AS INTEGER) AS amount_idr,
    CAST(COUNT(*) AS INTEGER)                       AS payment_count
FROM sale_payment p
JOIN sale s ON s.id = p.sale_id
WHERE s.cash_session_id = sqlc.arg(cash_session_id)
  AND s.status = 'FINAL'
GROUP BY p.method
ORDER BY p.method;

-- name: SumSessionCashRefunds :one
SELECT CAST(COALESCE(SUM(r.refund_idr), 0) AS INTEGER) AS refund_idr
FROM sale_return r
JOIN sale s ON s.id = r.sale_id
WHERE s.cash_session_id = sqlc.arg(cash_session_id)
  AND r.refund_method = 'TUNAI';

-- dpp_idr and ppn_idr come from domain/tax and the table CHECKs that they sum
-- to total_idr (SPEC 2.2). ppn_inclusive is snapshotted from the rule that
-- priced the sale, so a receipt reprinted next year breaks down the same way.
-- name: CreateSale :one
INSERT INTO sale (
    id, entity_id, cash_session_id, customer_id, invoice_no, occurred_at,
    business_date, status, faktur_issued, faktur_no, gross_idr, discount_idr,
    dpp_idr, ppn_idr, ppn_inclusive, total_idr, cogs_idr, is_credit, due_date,
    note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.narg(cash_session_id), sqlc.narg(customer_id),
    sqlc.arg(invoice_no), sqlc.arg(occurred_at), sqlc.arg(business_date), 'FINAL',
    sqlc.arg(faktur_issued), sqlc.narg(faktur_no), sqlc.arg(gross_idr), sqlc.arg(discount_idr),
    sqlc.arg(dpp_idr), sqlc.arg(ppn_idr), sqlc.arg(ppn_inclusive), sqlc.arg(total_idr),
    sqlc.arg(cogs_idr), sqlc.arg(is_credit),
    sqlc.narg(due_date), sqlc.narg(note), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- dpp_idr is this line's revenue for the margin report: COGS is already net of
-- creditable PPN (SPEC 3.2), so revenue has to be too or the two do not
-- compare. Under inclusive pricing net_idr contains the tax.
-- name: CreateSaleLine :one
INSERT INTO sale_line (
    id, sale_id, product_id, owner_id, qty, unit_price_idr, gross_idr,
    line_discount_idr, alloc_discount_idr, net_idr, dpp_idr, ppn_idr,
    cogs_idr, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(sale_id), sqlc.arg(product_id), sqlc.narg(owner_id),
    sqlc.arg(qty), sqlc.arg(unit_price_idr), sqlc.arg(gross_idr),
    sqlc.arg(line_discount_idr), sqlc.arg(alloc_discount_idr), sqlc.arg(net_idr),
    sqlc.arg(dpp_idr), sqlc.arg(ppn_idr), sqlc.arg(cogs_idr), sqlc.arg(created_at)
)
RETURNING *;

-- name: CreateSalePayment :one
INSERT INTO sale_payment (id, sale_id, method, amount_idr, reference, created_at)
VALUES (sqlc.arg(id), sqlc.arg(sale_id), sqlc.arg(method), sqlc.arg(amount_idr),
        sqlc.narg(reference), sqlc.arg(created_at))
RETURNING *;

-- name: GetSale :one
SELECT * FROM sale WHERE id = sqlc.arg(id);

-- name: GetSaleByInvoiceNo :one
SELECT * FROM sale WHERE entity_id = sqlc.arg(entity_id) AND invoice_no = sqlc.arg(invoice_no);

-- name: ListSales :many
SELECT * FROM sale
WHERE entity_id = sqlc.arg(entity_id)
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date)
ORDER BY occurred_at DESC, id DESC;

-- name: ListSaleLines :many
SELECT
    l.*,
    p.code AS product_code,
    p.name AS product_name,
    p.unit AS product_unit,
    o.name AS owner_name
FROM sale_line l
JOIN product p ON p.id = l.product_id
LEFT JOIN owner o ON o.id = l.owner_id
WHERE l.sale_id = sqlc.arg(sale_id)
ORDER BY l.created_at, l.id;

-- name: GetSaleLine :one
SELECT * FROM sale_line WHERE id = sqlc.arg(id);

-- name: ListSalePayments :many
SELECT * FROM sale_payment WHERE sale_id = sqlc.arg(sale_id) ORDER BY created_at, id;

-- Invoice numbers are per company and sequential within a business date, which
-- is what a shop expects to read off a receipt.
-- name: CountSalesOnDate :one
SELECT CAST(COUNT(*) AS INTEGER) AS sales_today
FROM sale
WHERE entity_id = sqlc.arg(entity_id) AND business_date = sqlc.arg(business_date);

-- The only UPDATE on sale, and the trigger allows no other shape.
-- name: VoidSale :one
UPDATE sale
SET status = 'VOID',
    voided_at = sqlc.arg(voided_at),
    voided_by = sqlc.narg(voided_by),
    void_reason = sqlc.arg(void_reason)
WHERE id = sqlc.arg(id) AND status = 'FINAL'
RETURNING *;

-- ppn_reversed_idr is output PPN handed back, prorated from the snapshot on the
-- original sale rather than recomputed against today's rate (INV-3). It nets
-- against output PPN in the position report, exactly as a purchase return nets
-- against creditable input.
-- name: CreateSaleReturn :one
INSERT INTO sale_return (
    id, entity_id, sale_id, occurred_at, business_date, sale_business_date,
    reason, refund_idr, cogs_reversed_idr, ppn_reversed_idr, refund_method,
    created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(sale_id), sqlc.arg(occurred_at),
    sqlc.arg(business_date), sqlc.arg(sale_business_date), sqlc.arg(reason),
    sqlc.arg(refund_idr), sqlc.arg(cogs_reversed_idr), sqlc.arg(ppn_reversed_idr),
    sqlc.arg(refund_method), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: CreateSaleReturnLine :one
INSERT INTO sale_return_line (
    id, sale_return_id, sale_line_id, qty, refund_idr, cogs_reversed_idr,
    ppn_reversed_idr, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(sale_return_id), sqlc.arg(sale_line_id), sqlc.arg(qty),
    sqlc.arg(refund_idr), sqlc.arg(cogs_reversed_idr), sqlc.arg(ppn_reversed_idr),
    sqlc.arg(created_at)
)
RETURNING *;

-- name: ListSaleReturns :many
SELECT * FROM sale_return WHERE sale_id = sqlc.arg(sale_id) ORDER BY occurred_at, id;

-- How much of one sale line has already come back, so a second return cannot
-- send back more than was sold.
-- name: SumReturnedForSaleLine :one
SELECT CAST(COALESCE(SUM(qty), 0) AS INTEGER) AS qty_returned
FROM sale_return_line
WHERE sale_line_id = sqlc.arg(sale_line_id);

-- The drill-down's first level (SPEC 4.2): the draws one sale made, with the
-- layer each came from. The consumption rows are the authority for COGS; the
-- figure on the sale header is a convenience for the sales report.
-- name: ListSaleConsumptions :many
SELECT
    c.*,
    l.product_id      AS product_id,
    l.owner_id        AS owner_id,
    l.acquired_at     AS layer_acquired_at,
    l.cost_total_idr  AS layer_cost_total_idr,
    l.qty_in          AS layer_qty_in,
    l.faktur_received AS layer_faktur_received
FROM stock_consumption c
JOIN stock_layer l ON l.id = c.layer_id
WHERE c.movement_id = sqlc.arg(movement_id)
ORDER BY l.acquired_at, l.id, c.created_at;

-- The draws a sale line made, in FIFO order, so a return can give them back
-- newest-first -- reversing the most recent draw before an older one keeps the
-- layer history reading in the order things actually happened.
-- name: ListDrawsForMovement :many
SELECT
    c.id            AS id,
    c.layer_id      AS layer_id,
    c.qty_out       AS qty_out,
    c.cost_idr      AS cost_idr,
    CAST(COALESCE((
        SELECT -SUM(r.qty_out) FROM stock_consumption r WHERE r.reverses_id = c.id
    ), 0) AS INTEGER) AS qty_reversed
FROM stock_consumption c
JOIN stock_layer l ON l.id = c.layer_id
WHERE c.movement_id = sqlc.arg(movement_id)
  AND c.reverses_id IS NULL
  AND l.product_id = sqlc.arg(product_id)
ORDER BY l.acquired_at DESC, l.id DESC, c.created_at DESC;
