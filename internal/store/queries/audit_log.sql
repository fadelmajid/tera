-- name: WriteAuditLog :exec
INSERT INTO audit_log (
    id, actor_user_id, occurred_at, legal_entity_id, record_type, record_id,
    action, before_json, after_json, reason, client_request_id
) VALUES (
    sqlc.arg(id), sqlc.narg(actor_user_id), sqlc.arg(occurred_at),
    sqlc.narg(legal_entity_id), sqlc.arg(record_type), sqlc.arg(record_id),
    sqlc.arg(action), sqlc.narg(before_json), sqlc.narg(after_json),
    sqlc.narg(reason), sqlc.narg(client_request_id)
);

-- name: ListAuditByPeriod :many
SELECT * FROM audit_log
WHERE occurred_at >= sqlc.arg(from_at) AND occurred_at < sqlc.arg(to_at)
ORDER BY occurred_at DESC, id DESC;

-- name: ListAuditByActor :many
SELECT * FROM audit_log
WHERE actor_user_id = sqlc.arg(actor_user_id)
  AND occurred_at >= sqlc.arg(from_at) AND occurred_at < sqlc.arg(to_at)
ORDER BY occurred_at DESC, id DESC;

-- The trail for one record: what happened to this sale, this layer, this product.
-- name: ListAuditForRecord :many
SELECT * FROM audit_log
WHERE record_type = sqlc.arg(record_type) AND record_id = sqlc.arg(record_id)
ORDER BY occurred_at DESC, id DESC;

-- name: CountAuditLog :one
SELECT COUNT(*) FROM audit_log;
