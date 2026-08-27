-- Inter-company stock transfer. TASKS 4.1-4.6, SPEC 3.4.
-- Keep this file ASCII-only -- see README.
--
-- A transfer is immutable (INV-2), so there is no UPDATE and no DELETE here.
-- A correction is a transfer the other way.

-- name: CreateTransfer :one
INSERT INTO transfer (
    id, from_entity_id, to_entity_id, transfer_no, occurred_at,
    business_date, to_business_date, from_is_pkp, to_is_pkp,
    credit_loss_ack, forfeited_ppn_idr,
    cost_total_idr, ppn_idr, amount_idr,
    faktur_issued, faktur_no, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(from_entity_id), sqlc.arg(to_entity_id), sqlc.arg(transfer_no),
    sqlc.arg(occurred_at), sqlc.arg(business_date), sqlc.arg(to_business_date),
    sqlc.arg(from_is_pkp), sqlc.arg(to_is_pkp),
    sqlc.arg(credit_loss_ack), sqlc.arg(forfeited_ppn_idr),
    sqlc.arg(cost_total_idr), sqlc.arg(ppn_idr), sqlc.arg(amount_idr),
    sqlc.arg(faktur_issued), sqlc.narg(faktur_no), sqlc.narg(note),
    sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: CreateTransferLine :one
INSERT INTO transfer_line (
    id, transfer_id, product_id, owner_id, qty,
    cost_total_idr, ppn_idr, forfeited_ppn_idr, dest_layer_id, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(transfer_id), sqlc.arg(product_id), sqlc.narg(owner_id),
    sqlc.arg(qty), sqlc.arg(cost_total_idr), sqlc.arg(ppn_idr),
    sqlc.arg(forfeited_ppn_idr), sqlc.arg(dest_layer_id), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetTransfer :one
SELECT * FROM transfer WHERE id = sqlc.arg(id);

-- Both sides of the boundary see it. A transfer is one document belonging to
-- two companies, so it is listed for either.
-- name: ListTransfers :many
SELECT
    t.*,
    f.name AS from_entity_name,
    d.name AS to_entity_name
FROM transfer t
JOIN legal_entity f ON f.id = t.from_entity_id
JOIN legal_entity d ON d.id = t.to_entity_id
WHERE t.from_entity_id = sqlc.arg(entity_id) OR t.to_entity_id = sqlc.arg(entity_id)
ORDER BY t.occurred_at DESC, t.id DESC;

-- name: ListTransferLines :many
SELECT
    l.*,
    p.code AS product_code,
    p.name AS product_name,
    p.unit AS product_unit,
    o.name AS owner_name
FROM transfer_line l
JOIN product p ON p.id = l.product_id
LEFT JOIN owner o ON o.id = l.owner_id
WHERE l.transfer_id = sqlc.arg(transfer_id)
ORDER BY l.created_at, l.id;

-- name: CountTransfersOnDate :one
SELECT CAST(COUNT(*) AS INTEGER) AS transfers_today
FROM transfer
WHERE from_entity_id = sqlc.arg(entity_id) AND business_date = sqlc.arg(business_date);

-- D-014: what each company owes the other, at cost, netted off. Deliberately
-- not hutang or piutang -- those are about suppliers and customers, and a
-- family's internal bookkeeping does not belong in a supplier aging report.
--
-- Grouped by counterparty rather than assuming there are two companies. There
-- are two today; the query does not need to know that.
-- name: InterCompanyPosition :many
SELECT
    -- CAST so sqlc infers string rather than interface{}: it cannot type a bare
    -- CASE, and an untyped id at the call site is a type assertion waiting to
    -- panic on a figure nobody is watching.
    CAST(CASE WHEN t.from_entity_id = sqlc.arg(entity_id) THEN t.to_entity_id ELSE t.from_entity_id END AS TEXT) AS counterparty_id,
    CAST(CASE WHEN t.from_entity_id = sqlc.arg(entity_id) THEN 'OUT' ELSE 'IN' END AS TEXT) AS leg,
    CAST(COUNT(*) AS INTEGER)                       AS transfer_count,
    CAST(COALESCE(SUM(t.amount_idr), 0) AS INTEGER) AS amount_idr
FROM transfer t
WHERE t.from_entity_id = sqlc.arg(entity_id) OR t.to_entity_id = sqlc.arg(entity_id)
GROUP BY counterparty_id, leg;

-- The PPN paid on a layer, so a transfer that destroys input credit can quote
-- what is actually being given up rather than a percentage of a guess (R4.5).
-- Read alongside the balances the FIFO consumption planner already loads.
-- name: ListLayerPPNForProduct :many
SELECT
    l.id           AS id,
    l.qty_in       AS qty_in,
    l.ppn_paid_idr AS ppn_paid_idr
FROM stock_layer l
WHERE l.entity_id = sqlc.arg(entity_id) AND l.product_id = sqlc.arg(product_id);
