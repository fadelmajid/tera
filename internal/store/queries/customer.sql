-- name: CreateCustomer :one
INSERT INTO customer (
    id, code, name, npwp, nik, address, phone, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(code), sqlc.arg(name), sqlc.narg(npwp),
    sqlc.narg(nik), sqlc.narg(address), sqlc.narg(phone),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetCustomer :one
SELECT * FROM customer WHERE id = sqlc.arg(id);

-- name: GetCustomerByCode :one
SELECT * FROM customer WHERE code = sqlc.arg(code);

-- name: ListCustomers :many
SELECT * FROM customer
WHERE is_active = 1 OR sqlc.arg(include_inactive) = 1
ORDER BY name;

-- name: UpdateCustomer :one
UPDATE customer
SET name = sqlc.arg(name),
    npwp = sqlc.narg(npwp),
    nik = sqlc.narg(nik),
    address = sqlc.narg(address),
    phone = sqlc.narg(phone),
    is_active = sqlc.arg(is_active),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;
