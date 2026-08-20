-- name: CreateSession :exec
INSERT INTO session (token_hash, user_id, created_at, last_seen_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(created_at),
        sqlc.arg(last_seen_at), sqlc.arg(expires_at));

-- name: GetSession :one
SELECT * FROM session WHERE token_hash = sqlc.arg(token_hash);

-- name: TouchSession :exec
UPDATE session SET last_seen_at = sqlc.arg(last_seen_at) WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteSession :exec
DELETE FROM session WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteSessionsForUser :exec
DELETE FROM session WHERE user_id = sqlc.arg(user_id);

-- name: DeleteExpiredSessions :exec
DELETE FROM session WHERE expires_at <= sqlc.arg(now);
