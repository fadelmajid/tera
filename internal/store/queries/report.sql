-- Sales, purchases and stock reports. TASKS 6.1-6.3, R5.2-5.4, R5.7.
-- Keep this file ASCII-only -- see README.
--
-- R5.7 is the user's own framing: reports can start simple provided the raw
-- data can be pulled. So these are honest roll-ups over a date range with a
-- drill-down to the documents underneath, and nothing more clever than that.
-- The export (TASKS 6.6) is what carries the fidelity requirement.
--
-- Unlike margin.sql, these aggregate in SQL. That file sums in Go because a
-- margin figure is settled on between family members and every step has to be
-- testable; a period total on a sales report is a sum of stored figures that
-- are themselves already checked, and reimplementing SUM in Go would add a
-- place for the two to disagree rather than remove one.
--
-- Voided sales are excluded everywhere. A void says the sale did not happen.

-- --- sales (TASKS 6.1, R5.4) ------------------------------------------------

-- name: SalesSummary :one
SELECT
    CAST(COUNT(*) AS INTEGER)                              AS sale_count,
    CAST(COALESCE(SUM(gross_idr), 0) AS INTEGER)           AS gross_idr,
    CAST(COALESCE(SUM(discount_idr), 0) AS INTEGER)        AS discount_idr,
    CAST(COALESCE(SUM(dpp_idr), 0) AS INTEGER)             AS dpp_idr,
    CAST(COALESCE(SUM(ppn_idr), 0) AS INTEGER)             AS ppn_idr,
    CAST(COALESCE(SUM(total_idr), 0) AS INTEGER)           AS total_idr,
    CAST(COALESCE(SUM(cogs_idr), 0) AS INTEGER)            AS cogs_idr,
    CAST(COALESCE(SUM(CASE WHEN is_credit = 1 THEN total_idr ELSE 0 END), 0) AS INTEGER)
                                                           AS credit_idr,
    CAST(COALESCE(SUM(CASE WHEN faktur_issued = 1 THEN total_idr ELSE 0 END), 0) AS INTEGER)
                                                           AS with_faktur_idr
FROM sale
WHERE entity_id = sqlc.arg(entity_id)
  AND status = 'FINAL'
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- Voids are counted, not hidden. A day with eleven voids is a training problem
-- or a till problem, and it is invisible on a report that only shows what
-- stuck.
-- name: SalesVoidSummary :one
SELECT
    CAST(COUNT(*) AS INTEGER)                    AS void_count,
    CAST(COALESCE(SUM(total_idr), 0) AS INTEGER) AS total_idr
FROM sale
WHERE entity_id = sqlc.arg(entity_id)
  AND status = 'VOID'
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- name: SalesReturnSummary :one
SELECT
    CAST(COUNT(*) AS INTEGER)                             AS return_count,
    CAST(COALESCE(SUM(refund_idr), 0) AS INTEGER)         AS refund_idr,
    CAST(COALESCE(SUM(ppn_reversed_idr), 0) AS INTEGER)   AS ppn_reversed_idr,
    CAST(COALESCE(SUM(cogs_reversed_idr), 0) AS INTEGER)  AS cogs_reversed_idr
FROM sale_return
WHERE entity_id = sqlc.arg(entity_id)
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- name: SalesByDay :many
SELECT
    business_date                                AS business_date,
    CAST(COUNT(*) AS INTEGER)                    AS sale_count,
    CAST(COALESCE(SUM(dpp_idr), 0) AS INTEGER)   AS dpp_idr,
    CAST(COALESCE(SUM(ppn_idr), 0) AS INTEGER)   AS ppn_idr,
    CAST(COALESCE(SUM(total_idr), 0) AS INTEGER) AS total_idr,
    CAST(COALESCE(SUM(cogs_idr), 0) AS INTEGER)  AS cogs_idr
FROM sale
WHERE entity_id = sqlc.arg(entity_id)
  AND status = 'FINAL'
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date)
GROUP BY business_date
ORDER BY business_date;

-- What actually sold, best first. The owner's question is which products move,
-- and revenue is the DPP for the same reason it is on the margin report: cost
-- is already net of creditable PPN (SPEC 3.2), so revenue has to be too.
-- name: SalesByProduct :many
SELECT
    l.product_id                                   AS product_id,
    p.code                                         AS product_code,
    p.name                                         AS product_name,
    p.unit                                         AS product_unit,
    l.owner_id                                     AS owner_id,
    o.name                                         AS owner_name,
    CAST(COALESCE(SUM(l.qty), 0) AS INTEGER)       AS qty,
    CAST(COALESCE(SUM(l.dpp_idr), 0) AS INTEGER)   AS revenue_idr,
    CAST(COALESCE(SUM(l.ppn_idr), 0) AS INTEGER)   AS ppn_idr,
    CAST(COALESCE(SUM(l.cogs_idr), 0) AS INTEGER)  AS cogs_idr
FROM sale_line l
JOIN sale s     ON s.id = l.sale_id
JOIN product p  ON p.id = l.product_id
LEFT JOIN owner o ON o.id = l.owner_id
WHERE s.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND s.business_date >= sqlc.arg(from_date)
  AND s.business_date <= sqlc.arg(to_date)
GROUP BY l.product_id, l.owner_id
ORDER BY revenue_idr DESC, p.name;

-- R9.10: non-cash methods are recorded, not processed. This is what makes the
-- recording worth anything -- how the shop is actually being paid.
-- name: SalesByPaymentMethod :many
SELECT
    pay.method                                          AS method,
    CAST(COUNT(*) AS INTEGER)                           AS payment_count,
    CAST(COALESCE(SUM(pay.amount_idr), 0) AS INTEGER)   AS amount_idr
FROM sale_payment pay
JOIN sale s ON s.id = pay.sale_id
WHERE s.entity_id = sqlc.arg(entity_id)
  AND s.status = 'FINAL'
  AND s.business_date >= sqlc.arg(from_date)
  AND s.business_date <= sqlc.arg(to_date)
GROUP BY pay.method
ORDER BY amount_idr DESC;

-- --- purchases (TASKS 6.2, R5.3) --------------------------------------------

-- name: PurchasesSummary :one
SELECT
    CAST(COUNT(*) AS INTEGER)                       AS purchase_count,
    CAST(COALESCE(SUM(subtotal_idr), 0) AS INTEGER) AS subtotal_idr,
    CAST(COALESCE(SUM(ppn_idr), 0) AS INTEGER)      AS ppn_idr,
    CAST(COALESCE(SUM(total_idr), 0) AS INTEGER)    AS total_idr,
    CAST(COALESCE(SUM(CASE WHEN faktur_received = 1 THEN total_idr ELSE 0 END), 0) AS INTEGER)
                                                    AS with_faktur_idr,
    CAST(COALESCE(SUM(CASE WHEN faktur_received = 0 THEN total_idr ELSE 0 END), 0) AS INTEGER)
                                                    AS without_faktur_idr,
    CAST(COALESCE(SUM(CASE WHEN faktur_received = 0 THEN ppn_idr ELSE 0 END), 0) AS INTEGER)
                                                    AS ppn_into_cost_idr
FROM purchase
WHERE entity_id = sqlc.arg(entity_id)
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- Supplier comparison, which is R10.6's whole point: the same goods from a
-- supplier who issues a faktur cost ~11% less in real terms at a PKP company,
-- and that difference is invisible on an invoice.
-- name: PurchasesBySupplier :many
SELECT
    p.supplier_id                                        AS supplier_id,
    s.code                                               AS supplier_code,
    s.name                                               AS supplier_name,
    s.issues_faktur                                      AS issues_faktur,
    CAST(COUNT(*) AS INTEGER)                            AS purchase_count,
    CAST(COALESCE(SUM(p.total_idr), 0) AS INTEGER)       AS total_idr,
    CAST(COALESCE(SUM(p.ppn_idr), 0) AS INTEGER)         AS ppn_idr,
    CAST(SUM(CASE WHEN p.faktur_received = 1 THEN 1 ELSE 0 END) AS INTEGER)
                                                         AS with_faktur_count,
    CAST(COALESCE(SUM(CASE WHEN p.faktur_received = 0 THEN p.ppn_idr ELSE 0 END), 0) AS INTEGER)
                                                         AS ppn_into_cost_idr
FROM purchase p
JOIN supplier s ON s.id = p.supplier_id
WHERE p.entity_id = sqlc.arg(entity_id)
  AND p.business_date >= sqlc.arg(from_date)
  AND p.business_date <= sqlc.arg(to_date)
GROUP BY p.supplier_id
ORDER BY total_idr DESC, s.name;

-- name: PurchasesByProduct :many
SELECT
    l.product_id                                        AS product_id,
    pr.code                                             AS product_code,
    pr.name                                             AS product_name,
    pr.unit                                             AS product_unit,
    CAST(COALESCE(SUM(l.qty), 0) AS INTEGER)            AS qty,
    CAST(COALESCE(SUM(l.gross_idr), 0) AS INTEGER)      AS gross_idr,
    -- What actually reached the stock layers, which is the figure every margin
    -- downstream is built on (SPEC 3.2).
    CAST(COALESCE(SUM(l.cost_total_idr), 0) AS INTEGER) AS cost_total_idr,
    CAST(COALESCE(SUM(l.creditable_ppn_idr), 0) AS INTEGER) AS creditable_ppn_idr
FROM purchase_line l
JOIN purchase p ON p.id = l.purchase_id
JOIN product pr ON pr.id = l.product_id
WHERE p.entity_id = sqlc.arg(entity_id)
  AND p.business_date >= sqlc.arg(from_date)
  AND p.business_date <= sqlc.arg(to_date)
GROUP BY l.product_id
ORDER BY cost_total_idr DESC, pr.name;

-- name: PurchaseReturnSummary :one
SELECT
    CAST(COUNT(*) AS INTEGER)                            AS return_count,
    CAST(COALESCE(SUM(cost_idr), 0) AS INTEGER)          AS cost_idr,
    CAST(COALESCE(SUM(ppn_reversed_idr), 0) AS INTEGER)  AS ppn_reversed_idr
FROM purchase_return
WHERE entity_id = sqlc.arg(entity_id)
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- --- stock (TASKS 6.3, R5.2) ------------------------------------------------

-- What is on the shelf, per product per owner, and what it is worth.
--
-- Value multiplies before it divides -- cost_total x remaining / qty_in, not a
-- rounded unit cost times a count, which loses rupiah on every layer (SPEC 1).
--
-- The division truncates, so a layer that does not divide evenly is worth up to
-- a rupiah less here than the sum of what its units will actually cost. That is
-- acceptable and it is only true of this figure: a valuation is an estimate of
-- stock nobody has sold yet, and the authoritative cost of any unit is decided
-- when it is drawn, by internal/domain/fifo, where the remainder is carried and
-- the last draw absorbs it. Nothing settles money on this column.
--
-- Owner is on the row because stock is owner-attributed and a total that mixes
-- two family members' goods is not a figure either of them can use (INV-8).
-- name: StockOnHand :many
SELECT
    b.product_id                                  AS product_id,
    p.code                                        AS product_code,
    p.name                                        AS product_name,
    p.unit                                        AS product_unit,
    p.category                                    AS category,
    b.owner_id                                    AS owner_id,
    o.name                                        AS owner_name,
    CAST(COALESCE(SUM(b.qty_remaining), 0) AS INTEGER) AS qty_on_hand,
    CAST(COALESCE(SUM(b.cost_total_idr * b.qty_remaining / b.qty_in), 0) AS INTEGER)
                                                  AS value_idr,
    CAST(COUNT(*) AS INTEGER)                     AS layer_count,
    CAST(SUM(CASE WHEN b.faktur_received = 0 THEN 1 ELSE 0 END) AS INTEGER)
                                                  AS layers_without_faktur,
    MIN(b.expiry_date)                            AS earliest_expiry
FROM stock_layer_balance b
JOIN product p ON p.id = b.product_id
LEFT JOIN owner o ON o.id = b.owner_id
WHERE b.entity_id = sqlc.arg(entity_id)
  AND b.qty_remaining > 0
GROUP BY b.product_id, b.owner_id
ORDER BY p.name, o.name;

-- Products with stock nowhere in this company. Not the same question as "what
-- is on the shelf" and it is the one that costs a sale: a catalogue entry the
-- cashier can scan and cannot sell.
-- name: StockOutOfStock :many
SELECT
    p.id       AS product_id,
    p.code     AS product_code,
    p.name     AS product_name,
    p.unit     AS product_unit,
    p.owner_id AS owner_id
FROM product p
WHERE p.is_active = 1
  AND NOT EXISTS (
      SELECT 1 FROM stock_layer_balance b
      WHERE b.product_id = p.id
        AND b.entity_id = sqlc.arg(entity_id)
        AND b.qty_remaining > 0
  )
ORDER BY p.name;

-- Everything that moved in the period, by product and reason.
--
-- qty_out is signed on purpose: a sales return is a negative draw against the
-- layer it came from (D-010), so summing without regard to sign would report
-- goods leaving twice.
-- name: StockMovementByProduct :many
SELECT
    l.product_id                                  AS product_id,
    p.code                                        AS product_code,
    p.name                                        AS product_name,
    c.movement_type                               AS movement_type,
    CAST(COALESCE(SUM(c.qty_out), 0) AS INTEGER)  AS qty,
    CAST(COALESCE(SUM(c.cost_idr), 0) AS INTEGER) AS cost_idr
FROM stock_consumption c
JOIN stock_layer l ON l.id = c.layer_id
JOIN product p     ON p.id = l.product_id
WHERE l.entity_id = sqlc.arg(entity_id)
  AND c.business_date >= sqlc.arg(from_date)
  AND c.business_date <= sqlc.arg(to_date)
GROUP BY l.product_id, c.movement_type
ORDER BY p.name, c.movement_type;

-- Stock arriving in the period, by reason: bought, transferred in, adjusted up,
-- or carried in at go-live.
-- name: StockIntakeByProduct :many
SELECT
    l.product_id                                       AS product_id,
    p.code                                             AS product_code,
    p.name                                             AS product_name,
    l.source                                           AS source,
    CAST(COALESCE(SUM(l.qty_in), 0) AS INTEGER)        AS qty,
    CAST(COALESCE(SUM(l.cost_total_idr), 0) AS INTEGER) AS cost_idr
FROM stock_layer l
JOIN product p ON p.id = l.product_id
WHERE l.entity_id = sqlc.arg(entity_id)
  AND l.business_date >= sqlc.arg(from_date)
  AND l.business_date <= sqlc.arg(to_date)
GROUP BY l.product_id, l.source
ORDER BY p.name, l.source;

-- --- hutang and piutang (TASKS 6.4-6.5, R5.5-5.6, R5.8) ---------------------

-- Everything still owed, with the counterparty named. Aging happens in
-- internal/domain/aging: which bucket a document falls in depends on rules --
-- what an absent due date means, what "overdue" means on the due date itself --
-- and those belong somewhere they can be tested exhaustively, not in a CASE
-- expression nobody can write a test against.
-- name: OutstandingPayablesForAging :many
SELECT
    b.id           AS id,
    b.supplier_id  AS counterparty_id,
    s.name         AS counterparty_name,
    b.source       AS source,
    b.invoice_no   AS invoice_no,
    b.amount_idr   AS amount_idr,
    b.paid_idr     AS paid_idr,
    b.outstanding_idr AS outstanding_idr,
    b.incurred_on  AS incurred_on,
    b.due_date     AS due_date
FROM payable_balance b
JOIN supplier s ON s.id = b.supplier_id
WHERE b.entity_id = sqlc.arg(entity_id)
  AND b.outstanding_idr <> 0
ORDER BY b.due_date IS NULL, b.due_date, b.incurred_on, b.id;

-- name: OutstandingReceivablesForAging :many
SELECT
    b.id           AS id,
    b.customer_id  AS counterparty_id,
    c.name         AS counterparty_name,
    b.source       AS source,
    b.invoice_no   AS invoice_no,
    b.amount_idr   AS amount_idr,
    b.paid_idr     AS paid_idr,
    b.outstanding_idr AS outstanding_idr,
    b.incurred_on  AS incurred_on,
    b.due_date     AS due_date
FROM receivable_balance b
JOIN customer c ON c.id = b.customer_id
WHERE b.entity_id = sqlc.arg(entity_id)
  AND b.outstanding_idr <> 0
ORDER BY b.due_date IS NULL, b.due_date, b.incurred_on, b.id;
