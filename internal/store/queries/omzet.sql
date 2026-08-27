-- The omzet clock. TASKS 7.1-7.11, SPEC 5.
-- Keep this file ASCII-only -- see README.
--
-- Nothing here aggregates. The ledger rows are loaded and internal/domain/omzet
-- does the arithmetic, for the same reason margin.sql does it that way: the
-- book-year window, the stickiness of a crossing, and the two dates it emits are
-- rules with edges, and SQL is the worst place to test a rule. A CASE expression
-- can express "crossed" -- nothing can write a test against it.

-- name: AppendOmzet :one
INSERT INTO omzet_ledger (
    id, entity_id, book_year, effective_date, event_type,
    signed_amount_idr, source_txn_id, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(book_year), sqlc.arg(effective_date),
    sqlc.arg(event_type), sqlc.arg(signed_amount_idr), sqlc.narg(source_txn_id),
    sqlc.narg(note), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- Every row the clock needs, across a range wide enough to cover both the book
-- year and the trailing twelve months. The service declares that range to the
-- domain, which refuses a request whose rows cannot cover what it is being
-- asked for -- a trailing figure that is quietly short is worse than none,
-- because it is the number somebody plans a year around.
-- name: ListOmzetEntries :many
SELECT * FROM omzet_ledger
WHERE entity_id = sqlc.arg(entity_id)
  AND effective_date >= sqlc.arg(from_date)
  AND effective_date <= sqlc.arg(to_date)
ORDER BY effective_date, id;

-- The documents behind one book year, newest first, for the drill-down.
-- name: ListOmzetForBookYear :many
SELECT
    l.id                AS id,
    l.book_year         AS book_year,
    l.effective_date    AS effective_date,
    l.event_type        AS event_type,
    l.signed_amount_idr AS signed_amount_idr,
    l.source_txn_id     AS source_txn_id,
    l.note              AS note,
    l.created_at        AS created_at,
    s.invoice_no        AS sale_invoice_no
FROM omzet_ledger l
LEFT JOIN sale s ON s.id = l.source_txn_id
WHERE l.entity_id = sqlc.arg(entity_id)
  AND l.book_year = sqlc.arg(book_year)
ORDER BY l.effective_date DESC, l.id DESC;

-- Which book years have any turnover at all, so the screen can offer a year
-- picker built from the data rather than from a guess about when trading began.
-- name: ListOmzetBookYears :many
SELECT DISTINCT book_year AS book_year
FROM omzet_ledger
WHERE entity_id = sqlc.arg(entity_id)
ORDER BY book_year DESC;

-- --- the threshold, effective-dated (INV-4) ---------------------------------

-- name: ListOmzetThresholds :many
SELECT * FROM omzet_threshold
WHERE entity_id = sqlc.arg(entity_id)
ORDER BY valid_from, id;

-- name: GetOmzetThreshold :one
SELECT * FROM omzet_threshold WHERE id = sqlc.arg(id);

-- name: CreateOmzetThreshold :one
INSERT INTO omzet_threshold (
    id, entity_id, amount_idr, watch_bp, warn_bp,
    register_by_policy, vat_starts_policy, valid_from, valid_to,
    legal_ref, note, created_by, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entity_id), sqlc.arg(amount_idr),
    sqlc.arg(watch_bp), sqlc.arg(warn_bp),
    sqlc.arg(register_by_policy), sqlc.arg(vat_starts_policy),
    sqlc.arg(valid_from), sqlc.narg(valid_to),
    sqlc.arg(legal_ref), sqlc.narg(note), sqlc.narg(created_by), sqlc.arg(created_at)
)
RETURNING *;

-- The only permitted edit (INV-4): close the window so a new row can open the
-- next day.
-- name: CloseOmzetThreshold :one
UPDATE omzet_threshold
SET valid_to = sqlc.arg(valid_to),
    closed_by = sqlc.narg(closed_by),
    closed_at = sqlc.arg(closed_at)
WHERE id = sqlc.arg(id) AND valid_to IS NULL
RETURNING *;

-- --- per-company settings ---------------------------------------------------

-- name: GetOmzetSetting :one
SELECT * FROM omzet_setting WHERE entity_id = sqlc.arg(entity_id);

-- A row means somebody chose; no row means the default (SPEC 5.4).
-- name: UpsertOmzetSetting :one
INSERT INTO omzet_setting (entity_id, base, note, updated_by, updated_at, created_at)
VALUES (
    sqlc.arg(entity_id), sqlc.arg(base), sqlc.narg(note),
    sqlc.narg(updated_by), sqlc.arg(updated_at), sqlc.arg(created_at)
)
ON CONFLICT (entity_id) DO UPDATE SET
    base       = excluded.base,
    note       = excluded.note,
    updated_by = excluded.updated_by,
    updated_at = excluded.updated_at
RETURNING *;

-- The rows one sale has produced, so a void or a return reverses exactly what
-- was counted rather than recomputing it from today's configuration. A base
-- setting changed between the sale and the return would otherwise leave a
-- difference in the clock with nothing to explain it.
-- name: ListOmzetForSource :many
SELECT * FROM omzet_ledger
WHERE source_txn_id = sqlc.narg(source_txn_id)
ORDER BY effective_date, id;
