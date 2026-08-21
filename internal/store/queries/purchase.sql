-- Purchases. Immutable once written (INV-2), so there is no UPDATE and no
-- DELETE here; a correction is a purchase return.
-- Keep this file ASCII-only -- see README.

-- name: CreatePurchase :one
INSERT INTO purchase (
    id, entity_id, supplier_id, invoice_no, occurred_at, business_date,
    faktur_received, faktur_no, subtotal_idr, ppn_idr, total_idr,
    is_credit, due_date, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(supplier_id), sqlc.narg(invoice_no),
    sqlc.arg(occurred_at), sqlc.arg(business_date),
    sqlc.arg(faktur_received), sqlc.narg(faktur_no),
    sqlc.arg(subtotal_idr), sqlc.arg(ppn_idr), sqlc.arg(total_idr),
    sqlc.arg(is_credit), sqlc.narg(due_date), sqlc.narg(note),
    sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: CreatePurchaseLine :one
INSERT INTO purchase_line (
    id, purchase_id, product_id, owner_id, qty, unit_price_idr,
    subtotal_idr, ppn_idr, gross_idr, cost_total_idr, creditable_ppn_idr,
    stock_layer_id, expiry_date, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(purchase_id), sqlc.arg(product_id), sqlc.narg(owner_id),
    sqlc.arg(qty), sqlc.arg(unit_price_idr), sqlc.arg(subtotal_idr), sqlc.arg(ppn_idr),
    sqlc.arg(gross_idr), sqlc.arg(cost_total_idr), sqlc.arg(creditable_ppn_idr),
    sqlc.arg(stock_layer_id), sqlc.narg(expiry_date), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetPurchase :one
SELECT * FROM purchase WHERE id = sqlc.arg(id);

-- name: ListPurchases :many
SELECT * FROM purchase
WHERE entity_id = sqlc.arg(entity_id)
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date)
ORDER BY business_date DESC, created_at DESC;

-- name: ListPurchaseLines :many
SELECT
    l.*,
    p.code AS product_code,
    p.name AS product_name,
    p.unit AS product_unit
FROM purchase_line l
JOIN product p ON p.id = l.product_id
WHERE l.purchase_id = sqlc.arg(purchase_id)
ORDER BY l.created_at, l.id;

-- name: GetPurchaseLine :one
SELECT * FROM purchase_line WHERE id = sqlc.arg(id);

-- The input side of the PPN position (SPEC 2.4): purchases WHERE the faktur was
-- received. Purchases without one contribute nothing here -- their PPN went
-- into the cost layer instead. The filter is the whole point of the report.
-- name: SumCreditableInputPPN :one
SELECT CAST(COALESCE(SUM(l.creditable_ppn_idr), 0) AS INTEGER) AS creditable_ppn_idr
FROM purchase_line l
JOIN purchase p ON p.id = l.purchase_id
WHERE p.entity_id = sqlc.arg(entity_id)
  AND p.faktur_received = 1
  AND p.business_date >= sqlc.arg(from_date)
  AND p.business_date <= sqlc.arg(to_date);

-- Input PPN handed back when goods went back to the supplier (R12.2). Netted
-- off the figure above; claiming credit on returned goods is the error.
-- name: SumReversedInputPPN :one
SELECT CAST(COALESCE(SUM(ppn_reversed_idr), 0) AS INTEGER) AS ppn_reversed_idr
FROM purchase_return
WHERE entity_id = sqlc.arg(entity_id)
  AND business_date >= sqlc.arg(from_date)
  AND business_date <= sqlc.arg(to_date);

-- name: CreatePurchaseReturn :one
INSERT INTO purchase_return (
    id, entity_id, purchase_id, occurred_at, business_date, reason,
    cost_idr, ppn_reversed_idr, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(purchase_id), sqlc.arg(occurred_at),
    sqlc.arg(business_date), sqlc.arg(reason), sqlc.arg(cost_idr),
    sqlc.arg(ppn_reversed_idr), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: CreatePurchaseReturnLine :one
INSERT INTO purchase_return_line (
    id, purchase_return_id, purchase_line_id, stock_layer_id, consumption_id,
    qty, cost_idr, ppn_reversed_idr, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(purchase_return_id), sqlc.arg(purchase_line_id),
    sqlc.arg(stock_layer_id), sqlc.arg(consumption_id), sqlc.arg(qty),
    sqlc.arg(cost_idr), sqlc.arg(ppn_reversed_idr), sqlc.arg(created_at)
)
RETURNING *;

-- name: ListPurchaseReturns :many
SELECT * FROM purchase_return
WHERE purchase_id = sqlc.arg(purchase_id)
ORDER BY occurred_at, id;

-- How much of one purchase line has already gone back, so a second return
-- cannot send back more than ever arrived.
-- name: SumReturnedForPurchaseLine :one
SELECT CAST(COALESCE(SUM(qty), 0) AS INTEGER) AS qty_returned
FROM purchase_return_line
WHERE purchase_line_id = sqlc.arg(purchase_line_id);
