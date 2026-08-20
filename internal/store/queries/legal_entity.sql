-- name: CreateLegalEntity :one
INSERT INTO legal_entity (
    id, code, name, is_pkp, npwp, address, phone,
    timezone, book_year_start_month, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(code), sqlc.arg(name), sqlc.arg(is_pkp),
    sqlc.narg(npwp), sqlc.narg(address), sqlc.narg(phone),
    sqlc.arg(timezone), sqlc.arg(book_year_start_month),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetLegalEntity :one
SELECT * FROM legal_entity WHERE id = sqlc.arg(id);

-- name: GetLegalEntityByCode :one
SELECT * FROM legal_entity WHERE code = sqlc.arg(code);

-- name: ListLegalEntities :many
SELECT * FROM legal_entity ORDER BY code;

-- name: UpdateLegalEntity :one
UPDATE legal_entity
SET name = sqlc.arg(name),
    is_pkp = sqlc.arg(is_pkp),
    npwp = sqlc.narg(npwp),
    address = sqlc.narg(address),
    phone = sqlc.narg(phone),
    timezone = sqlc.arg(timezone),
    book_year_start_month = sqlc.arg(book_year_start_month),
    is_active = sqlc.arg(is_active),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;
