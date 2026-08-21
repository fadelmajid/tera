-- FIFO inventory. Both tables are append-only (INV-7), so there is no UPDATE
-- and no DELETE in this file, and there never should be. Corrections are
-- compensating rows (INV-2, D-010). The triggers in migration 005 enforce it,
-- but the absence here is the first thing a reader should notice.

-- name: CreateStockLayer :one
INSERT INTO stock_layer (
    id, entity_id, product_id, owner_id, acquired_at, business_date,
    source, source_doc_id, qty_in, cost_total_idr,
    faktur_received, ppn_paid_idr, expiry_date, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(product_id), sqlc.narg(owner_id),
    sqlc.arg(acquired_at), sqlc.arg(business_date),
    sqlc.arg(source), sqlc.narg(source_doc_id), sqlc.arg(qty_in), sqlc.arg(cost_total_idr),
    sqlc.arg(faktur_received), sqlc.arg(ppn_paid_idr), sqlc.narg(expiry_date), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetStockLayer :one
SELECT * FROM stock_layer WHERE id = sqlc.arg(id);

-- The FIFO candidate set for a draw, oldest first, ties broken by id (SPEC 3.3).
--
-- Deliberately NOT filtered by owner. internal/domain/fifo does the owner
-- scoping itself and will only ever draw from matching layers (INV-8); handing
-- it every owner's layers is what lets an insufficient-stock error report how
-- much stock other owners hold -- the difference between an error a shopkeeper
-- can act on and one that looks like a bug with a full shelf in view.
--
-- Exhausted layers are dropped because they contribute nothing either way. They
-- stay in the table forever regardless; nothing here deletes.
-- name: ListLayersForConsumption :many
SELECT * FROM stock_layer_balance
WHERE entity_id = sqlc.arg(entity_id)
  AND product_id = sqlc.arg(product_id)
  AND qty_remaining > 0
ORDER BY acquired_at, id;

-- name: GetLayerBalance :one
SELECT * FROM stock_layer_balance WHERE id = sqlc.arg(id);

-- Stock on hand for one owner, per product. The company bucket is a distinct
-- query below rather than a nullable parameter, matching how the margin report
-- treats it (R2.2).
-- name: ListLayerBalancesByOwner :many
SELECT * FROM stock_layer_balance
WHERE entity_id = sqlc.arg(entity_id)
  AND owner_id = sqlc.arg(owner_id)
  AND qty_remaining > 0
ORDER BY product_id, acquired_at, id;

-- name: ListCompanyBucketLayerBalances :many
SELECT * FROM stock_layer_balance
WHERE entity_id = sqlc.arg(entity_id)
  AND owner_id IS NULL
  AND qty_remaining > 0
ORDER BY product_id, acquired_at, id;

-- The layers one purchase created. Needed to reverse it (R12.2).
-- name: ListLayersBySourceDoc :many
SELECT * FROM stock_layer
WHERE source = sqlc.arg(source) AND source_doc_id = sqlc.arg(source_doc_id)
ORDER BY acquired_at, id;

-- name: RecordConsumption :one
INSERT INTO stock_consumption (
    id, layer_id, movement_id, movement_type,
    qty_out, cost_idr, occurred_at, business_date, reverses_id, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(layer_id), sqlc.arg(movement_id), sqlc.arg(movement_type),
    sqlc.arg(qty_out), sqlc.arg(cost_idr), sqlc.arg(occurred_at),
    sqlc.arg(business_date), sqlc.narg(reverses_id), sqlc.arg(created_at)
)
RETURNING *;

-- The drill-down, one level down: which layers did this sale draw from, and
-- what did each cost (SPEC 4.2). Joined to the layer so the answer carries the
-- owner and the faktur status that set the cost basis.
-- name: ListConsumptionsForMovement :many
SELECT
    c.*,
    l.product_id      AS product_id,
    l.owner_id        AS owner_id,
    l.acquired_at     AS layer_acquired_at,
    l.qty_in          AS layer_qty_in,
    l.cost_total_idr  AS layer_cost_total_idr,
    l.faktur_received AS layer_faktur_received
FROM stock_consumption c
JOIN stock_layer l ON l.id = c.layer_id
WHERE c.movement_id = sqlc.arg(movement_id)
ORDER BY l.acquired_at, l.id, c.created_at;

-- name: ListConsumptionsForLayer :many
SELECT * FROM stock_consumption
WHERE layer_id = sqlc.arg(layer_id)
ORDER BY occurred_at, id;

-- D-010: the reversals against one draw cannot exceed what it took. Returning
-- four of three units sold is data entry to reject, not a correction. SQL
-- cannot express that as a row check, so the service reads this and decides.
-- name: SumReversedAgainstConsumption :one
SELECT CAST(COALESCE(-SUM(qty_out), 0) AS INTEGER) AS qty_reversed
FROM stock_consumption
WHERE reverses_id = sqlc.arg(reverses_id);
