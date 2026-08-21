-- Hutang and piutang. TASKS 1.11, R5.5-5.6, R5.8, R11.5.
-- Balances are derived from the payment rows, never stored.
-- Keep this file ASCII-only -- see README.

-- name: CreatePayable :one
INSERT INTO payable (
    id, entity_id, supplier_id, source, purchase_id, invoice_no,
    amount_idr, incurred_on, due_date, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(supplier_id), sqlc.arg(source),
    sqlc.narg(purchase_id), sqlc.narg(invoice_no), sqlc.arg(amount_idr),
    sqlc.arg(incurred_on), sqlc.narg(due_date), sqlc.narg(note),
    sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetPayable :one
SELECT * FROM payable_balance WHERE id = sqlc.arg(id);

-- name: ListOutstandingPayables :many
SELECT * FROM payable_balance
WHERE entity_id = sqlc.arg(entity_id)
  AND outstanding_idr > 0
ORDER BY due_date IS NULL, due_date, incurred_on;

-- name: ListPayables :many
SELECT * FROM payable_balance
WHERE entity_id = sqlc.arg(entity_id)
ORDER BY incurred_on DESC, id DESC;

-- name: RecordPayablePayment :one
INSERT INTO payable_payment (
    id, payable_id, amount_idr, paid_on, method, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(payable_id), sqlc.arg(amount_idr), sqlc.arg(paid_on),
    sqlc.arg(method), sqlc.narg(note), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: ListPayablePayments :many
SELECT * FROM payable_payment
WHERE payable_id = sqlc.arg(payable_id)
ORDER BY paid_on, id;

-- name: CreateReceivable :one
INSERT INTO receivable (
    id, entity_id, customer_id, source, sale_id, invoice_no,
    amount_idr, incurred_on, due_date, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(customer_id), sqlc.arg(source),
    sqlc.narg(sale_id), sqlc.narg(invoice_no), sqlc.arg(amount_idr),
    sqlc.arg(incurred_on), sqlc.narg(due_date), sqlc.narg(note),
    sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetReceivable :one
SELECT * FROM receivable_balance WHERE id = sqlc.arg(id);

-- name: ListOutstandingReceivables :many
SELECT * FROM receivable_balance
WHERE entity_id = sqlc.arg(entity_id)
  AND outstanding_idr > 0
ORDER BY due_date IS NULL, due_date, incurred_on;

-- name: ListReceivables :many
SELECT * FROM receivable_balance
WHERE entity_id = sqlc.arg(entity_id)
ORDER BY incurred_on DESC, id DESC;

-- name: RecordReceivablePayment :one
INSERT INTO receivable_payment (
    id, receivable_id, amount_idr, paid_on, method, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(receivable_id), sqlc.arg(amount_idr), sqlc.arg(paid_on),
    sqlc.arg(method), sqlc.narg(note), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- name: ListReceivablePayments :many
SELECT * FROM receivable_payment
WHERE receivable_id = sqlc.arg(receivable_id)
ORDER BY paid_on, id;
