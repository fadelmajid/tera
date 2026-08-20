-- name: CreateUser :one
INSERT INTO app_user (id, username, full_name, password_hash, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(username), sqlc.arg(full_name),
        sqlc.arg(password_hash), sqlc.arg(created_at), sqlc.arg(updated_at))
RETURNING *;

-- name: GetUser :one
SELECT * FROM app_user WHERE id = sqlc.arg(id);

-- name: GetUserByUsername :one
SELECT * FROM app_user WHERE username = sqlc.arg(username);

-- name: ListUsers :many
SELECT * FROM app_user ORDER BY username;

-- name: CountUsers :one
SELECT COUNT(*) FROM app_user;

-- name: SetUserPassword :exec
UPDATE app_user
SET password_hash = sqlc.arg(password_hash), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetUserActive :exec
UPDATE app_user
SET is_active = sqlc.arg(is_active), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: GrantRole :exec
INSERT INTO user_entity_role (user_id, entity_id, role, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(entity_id), sqlc.arg(role), sqlc.arg(created_at))
ON CONFLICT (user_id, entity_id) DO UPDATE SET role = excluded.role;

-- name: RevokeRole :exec
DELETE FROM user_entity_role
WHERE user_id = sqlc.arg(user_id) AND entity_id = sqlc.arg(entity_id);

-- name: ListRolesForUser :many
SELECT entity_id, role FROM user_entity_role WHERE user_id = sqlc.arg(user_id);

-- name: GetRoleForUserInEntity :one
SELECT role FROM user_entity_role
WHERE user_id = sqlc.arg(user_id) AND entity_id = sqlc.arg(entity_id);
