-- name: InsertGasRefill :one
INSERT INTO gas_refills (chain, order_uid, wallet_address, sell_amount, buy_amount, status, user_id, chat_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListPendingGasRefills :many
SELECT id, chain, order_uid, wallet_address, sell_amount, buy_amount, status, user_id, chat_id, created_at
FROM gas_refills WHERE status = 'open' ORDER BY created_at;

-- name: UpdateGasRefillStatus :exec
UPDATE gas_refills SET status = ? WHERE id = ?;
