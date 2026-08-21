-- Stock opname. TASKS 1.10, R12.4-5. Keep this file ASCII-only -- see README.

-- name: CreateOpname :one
INSERT INTO stock_opname (
    id, entity_id, status, counted_at, business_date, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), 'DRAFT', sqlc.arg(counted_at),
    sqlc.arg(business_date), sqlc.narg(note), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetOpname :one
SELECT * FROM stock_opname WHERE id = sqlc.arg(id);

-- name: ListOpnames :many
SELECT * FROM stock_opname
WHERE entity_id = sqlc.arg(entity_id)
ORDER BY counted_at DESC, id DESC;

-- name: MarkOpnamePosted :one
UPDATE stock_opname
SET status = 'POSTED', posted_at = sqlc.arg(posted_at), posted_by = sqlc.narg(posted_by)
WHERE id = sqlc.arg(id) AND status = 'DRAFT'
RETURNING *;

-- name: UpsertOpnameLine :one
INSERT INTO stock_opname_line (
    id, opname_id, product_id, owner_id, system_qty, counted_qty, variance,
    reason_code, reason_note, unit_cost_idr, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(opname_id), sqlc.arg(product_id), sqlc.narg(owner_id),
    sqlc.arg(system_qty), sqlc.arg(counted_qty), sqlc.arg(variance),
    sqlc.narg(reason_code), sqlc.narg(reason_note), sqlc.narg(unit_cost_idr),
    sqlc.arg(created_at)
)
ON CONFLICT (opname_id, product_id, owner_id) DO UPDATE SET
    system_qty    = excluded.system_qty,
    counted_qty   = excluded.counted_qty,
    variance      = excluded.variance,
    reason_code   = excluded.reason_code,
    reason_note   = excluded.reason_note,
    unit_cost_idr = excluded.unit_cost_idr
RETURNING *;

-- The variance report (R12.4). Joined to product and owner so the person
-- signing it off sees names, not ids.
-- name: ListOpnameLines :many
SELECT
    l.*,
    p.code AS product_code,
    p.name AS product_name,
    p.unit AS product_unit,
    o.name AS owner_name
FROM stock_opname_line l
JOIN product p ON p.id = l.product_id
LEFT JOIN owner o ON o.id = l.owner_id
WHERE l.opname_id = sqlc.arg(opname_id)
ORDER BY p.name, o.name;

-- name: DeleteOpnameLine :exec
DELETE FROM stock_opname_line WHERE id = sqlc.arg(id);

-- name: CreateOpnamePosting :one
INSERT INTO stock_opname_posting (
    id, opname_line_id, stock_layer_id, consumption_id, qty, cost_idr, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(opname_line_id), sqlc.narg(stock_layer_id),
    sqlc.narg(consumption_id), sqlc.arg(qty), sqlc.arg(cost_idr), sqlc.arg(created_at)
)
RETURNING *;

-- name: ListOpnamePostings :many
SELECT po.*
FROM stock_opname_posting po
JOIN stock_opname_line l ON l.id = po.opname_line_id
WHERE l.opname_id = sqlc.arg(opname_id)
ORDER BY po.created_at, po.id;

-- Everything currently on hand in one company, grouped the way a count is
-- taken: per product, per owner. This is what the count sheet is generated
-- from and what system_qty is snapshotted from.
-- name: ListStockOnHandByOwner :many
SELECT
    b.product_id                                       AS product_id,
    b.owner_id                                         AS owner_id,
    p.code                                             AS product_code,
    p.name                                             AS product_name,
    p.unit                                             AS product_unit,
    o.name                                             AS owner_name,
    CAST(SUM(b.qty_remaining) AS INTEGER)              AS qty_on_hand
FROM stock_layer_balance b
JOIN product p ON p.id = b.product_id
LEFT JOIN owner o ON o.id = b.owner_id
WHERE b.entity_id = sqlc.arg(entity_id)
  AND b.qty_remaining > 0
GROUP BY b.product_id, b.owner_id
ORDER BY p.name, o.name;

-- On-hand for exactly one (product, owner). The count sheet snapshots this, and
-- posting re-reads it to refuse a shortfall bigger than what is actually there.
-- name: GetStockOnHandForOwner :one
SELECT CAST(COALESCE(SUM(qty_remaining), 0) AS INTEGER) AS qty_on_hand
FROM stock_layer_balance
WHERE entity_id = sqlc.arg(entity_id)
  AND product_id = sqlc.arg(product_id)
  AND owner_id IS sqlc.narg(owner_id);

-- The unit cost a surplus line defaults to: the most recent layer of the same
-- product and owner. Found stock is nearly always a miscounted recent delivery,
-- so that layer is the best available answer. Derived from the layer total and
-- quantity, never a stored per-unit figure (SPEC 1).
-- name: LatestLayerUnitCost :one
SELECT CAST(cost_total_idr / qty_in AS INTEGER) AS unit_cost_idr
FROM stock_layer
WHERE entity_id = sqlc.arg(entity_id)
  AND product_id = sqlc.arg(product_id)
  AND owner_id IS sqlc.narg(owner_id)
ORDER BY acquired_at DESC, id DESC
LIMIT 1;
