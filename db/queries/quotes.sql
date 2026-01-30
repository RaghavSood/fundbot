-- name: InsertQuote :one
INSERT INTO quotes (
    type, provider, user_id, from_asset, from_chain, to_asset, destination,
    input_amount_usd, input_amount, expected_output, memo, router, vault_address, expiry, chat_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetQuote :one
SELECT id, type, provider, user_id, from_asset, from_chain, to_asset, destination,
    input_amount_usd, input_amount, expected_output, memo, router, vault_address, expiry, chat_id, created_at
FROM quotes
WHERE id = ?;
