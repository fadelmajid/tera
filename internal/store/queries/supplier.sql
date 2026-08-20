-- name: CreateSupplier :one
INSERT INTO supplier (
    id, code, name, npwp, address, phone, issues_faktur, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(code), sqlc.arg(name), sqlc.narg(npwp),
    sqlc.narg(address), sqlc.narg(phone), sqlc.arg(issues_faktur),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetSupplier :one
SELECT * FROM supplier WHERE id = sqlc.arg(id);

-- name: GetSupplierByCode :one
SELECT * FROM supplier WHERE code = sqlc.arg(code);

-- name: ListSuppliers :many
SELECT * FROM supplier
WHERE is_active = 1 OR sqlc.arg(include_inactive) = 1
ORDER BY name;

-- name: UpdateSupplier :one
UPDATE supplier
SET name = sqlc.arg(name),
    npwp = sqlc.narg(npwp),
    address = sqlc.narg(address),
    phone = sqlc.narg(phone),
    issues_faktur = sqlc.arg(issues_faktur),
    is_active = sqlc.arg(is_active),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;
