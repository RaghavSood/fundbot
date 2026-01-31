-- name: CountUsers :one
SELECT (SELECT COUNT(*) FROM users) + (SELECT COUNT(*) FROM chats);

-- name: CountTopups :one
SELECT COUNT(*) FROM topups;

-- name: TotalVolumeUSD :one
SELECT COALESCE(SUM(q.input_amount_usd), 0) FROM topups t JOIN quotes q ON t.quote_id = q.id;

-- name: CountDistinctPairs :one
SELECT COUNT(DISTINCT q.from_asset || '->' || q.to_asset) FROM topups t JOIN quotes q ON t.quote_id = q.id;

-- name: CountDistinctProviders :one
SELECT COUNT(DISTINCT provider) FROM topups;

-- name: ListRecentTopups :many
SELECT t.id, t.short_id, t.type, t.quote_id, t.user_id, t.provider, t.from_chain,
       t.tx_hash, t.status, t.created_at,
       q.from_asset, q.to_asset, q.destination, q.input_amount_usd, q.expected_output
FROM topups t JOIN quotes q ON t.quote_id = q.id
ORDER BY t.created_at DESC LIMIT ? OFFSET ?;

-- name: ListUsers :many
SELECT id, telegram_id, username, created_at FROM users ORDER BY id;

-- name: GetTopupsByUserID :many
SELECT t.id, t.short_id, t.type, t.quote_id, t.user_id, t.provider, t.from_chain,
       t.tx_hash, t.status, t.created_at
FROM topups t WHERE t.user_id = ? ORDER BY t.created_at DESC;

-- name: VolumeByToAsset :many
SELECT q.to_asset, COALESCE(SUM(q.input_amount_usd), 0) as total_usd, COUNT(*) as tx_count
FROM topups t JOIN quotes q ON t.quote_id = q.id
GROUP BY q.to_asset ORDER BY total_usd DESC;

-- name: VolumeByFromChain :many
SELECT t.from_chain, COALESCE(SUM(q.input_amount_usd), 0) as total_usd, COUNT(*) as tx_count
FROM topups t JOIN quotes q ON t.quote_id = q.id
GROUP BY t.from_chain ORDER BY total_usd DESC;

-- name: VolumeByDay :many
SELECT DATE(t.created_at) as day, COALESCE(SUM(q.input_amount_usd), 0) as total_usd, COUNT(*) as tx_count
FROM topups t JOIN quotes q ON t.quote_id = q.id
GROUP BY DATE(t.created_at) ORDER BY day;

-- name: VolumeByProvider :many
SELECT t.provider, COALESCE(SUM(q.input_amount_usd), 0) as total_usd, COUNT(*) as tx_count
FROM topups t JOIN quotes q ON t.quote_id = q.id
GROUP BY t.provider ORDER BY total_usd DESC;
