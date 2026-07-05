-- name: InsertTreasuryLedger :one
INSERT INTO treasury_ledger (direction, kind, chain, amount_usdc, tx_hash, deposit_address, topup_id, status, note)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListTreasuryLedger :many
SELECT id, direction, kind, chain, amount_usdc, tx_hash, deposit_address, topup_id, status, note, created_at
FROM treasury_ledger
ORDER BY created_at DESC, id DESC
LIMIT @limit OFFSET @offset;

-- name: CountTreasuryLedger :one
SELECT COUNT(*) FROM treasury_ledger;

-- name: ListPendingTreasuryOps :many
SELECT id, direction, kind, chain, amount_usdc, tx_hash, deposit_address, topup_id, status, note, created_at
FROM treasury_ledger
WHERE status = 'pending' AND deposit_address != ''
ORDER BY created_at;

-- name: UpdateTreasuryLedgerStatus :exec
UPDATE treasury_ledger SET status = ? WHERE id = ?;
