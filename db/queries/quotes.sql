-- name: InsertQuote :one
INSERT INTO quotes (
    type, provider, user_id, from_asset, from_chain, to_asset, destination,
    input_amount_usd, input_amount, expected_output, memo, router, vault_address, expiry
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetQuote :one
SELECT id, type, provider, user_id, from_asset, from_chain, to_asset, destination,
    input_amount_usd, input_amount, expected_output, memo, router, vault_address, expiry, created_at
FROM quotes
WHERE id = ?;
