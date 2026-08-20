-- name: CreateProduct :one
INSERT INTO product (
    id, code, barcode, name, unit, category, owner_id,
    sale_price_idr, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(code), sqlc.narg(barcode), sqlc.arg(name),
    sqlc.arg(unit), sqlc.narg(category), sqlc.narg(owner_id),
    sqlc.arg(sale_price_idr), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetProduct :one
SELECT * FROM product WHERE id = sqlc.arg(id);

-- name: GetProductByCode :one
SELECT * FROM product WHERE code = sqlc.arg(code);

-- Barcode lookup for the scanner (R9.3). The unique partial index makes this
-- a single row or none.
-- name: GetProductByBarcode :one
SELECT * FROM product WHERE barcode = sqlc.arg(barcode);

-- name: ListProducts :many
SELECT * FROM product
WHERE (is_active = 1 OR sqlc.arg(include_inactive) = 1)
ORDER BY name;

-- name: ListProductsByOwner :many
SELECT * FROM product
WHERE is_active = 1
  AND owner_id = sqlc.arg(owner_id)
ORDER BY name;

-- Products with no owner are the company bucket (R2.2). It is a distinct line
-- on the margin report, so it gets a distinct query rather than a nullable
-- parameter that reads as an afterthought.
-- name: ListCompanyBucketProducts :many
SELECT * FROM product
WHERE is_active = 1
  AND owner_id IS NULL
ORDER BY name;

-- name: UpdateProduct :one
UPDATE product
SET code = sqlc.arg(code),
    barcode = sqlc.narg(barcode),
    name = sqlc.arg(name),
    unit = sqlc.arg(unit),
    category = sqlc.narg(category),
    owner_id = sqlc.narg(owner_id),
    sale_price_idr = sqlc.arg(sale_price_idr),
    is_active = sqlc.arg(is_active),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;
