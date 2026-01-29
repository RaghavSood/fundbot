-- name: GetUserByTelegramID :one
SELECT id, telegram_id, username, created_at
FROM users
WHERE telegram_id = ?;

-- name: CreateUser :one
INSERT INTO users (telegram_id, username)
VALUES (?, ?)
RETURNING id, telegram_id, username, created_at;
