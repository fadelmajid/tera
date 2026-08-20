-- name: CreateOwner :one
INSERT INTO owner (id, code, name, note, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(code), sqlc.arg(name), sqlc.narg(note),
        sqlc.arg(created_at), sqlc.arg(updated_at))
RETURNING *;

-- name: GetOwner :one
SELECT * FROM owner WHERE id = sqlc.arg(id);

-- name: GetOwnerByCode :one
SELECT * FROM owner WHERE code = sqlc.arg(code);

-- name: ListOwners :many
SELECT * FROM owner
WHERE is_active = 1 OR sqlc.arg(include_inactive) = 1
ORDER BY name;

-- name: UpdateOwner :one
UPDATE owner
SET name = sqlc.arg(name),
    note = sqlc.narg(note),
    is_active = sqlc.arg(is_active),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;
