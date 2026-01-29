-- name: InsertTopup :one
INSERT INTO topups (short_id, type, quote_id, user_id, provider, from_chain, tx_hash, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, short_id;

-- name: GetTopupByShortID :one
SELECT id, short_id, type, quote_id, user_id, provider, from_chain, tx_hash, status, created_at
FROM topups
WHERE short_id = ?;

-- name: UpdateTopupStatus :exec
UPDATE topups SET status = ? WHERE id = ?;
