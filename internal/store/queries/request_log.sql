-- Claims an id. Returns the number of rows inserted: 1 means this caller owns
-- the request, 0 means someone else already claimed it.
-- name: ClaimRequest :execrows
INSERT INTO request_log (
    client_request_id, user_id, method, path, request_hash, status_code, response_body, created_at
) VALUES (
    sqlc.arg(client_request_id), sqlc.narg(user_id), sqlc.arg(method),
    sqlc.arg(path), sqlc.arg(request_hash), 0, '', sqlc.arg(created_at)
)
ON CONFLICT (client_request_id) DO NOTHING;

-- name: GetRequest :one
SELECT * FROM request_log WHERE client_request_id = sqlc.arg(client_request_id);

-- name: CompleteRequest :exec
UPDATE request_log
SET status_code = sqlc.arg(status_code),
    response_body = sqlc.arg(response_body),
    completed_at = sqlc.arg(completed_at)
WHERE client_request_id = sqlc.arg(client_request_id);

-- name: ReleaseRequest :exec
DELETE FROM request_log WHERE client_request_id = sqlc.arg(client_request_id);

-- Frees claims abandoned by a crash, so a retry is not blocked forever.
-- name: ReleaseStaleClaims :exec
DELETE FROM request_log
WHERE status_code = 0 AND created_at < sqlc.arg(older_than);
