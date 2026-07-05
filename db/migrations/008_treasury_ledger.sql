-- +goose Up
CREATE TABLE treasury_ledger (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    direction TEXT NOT NULL,                    -- 'in' | 'out'
    kind TEXT NOT NULL,                         -- 'user_payment' | 'intents_deposit' | 'shield' | 'confidential_swap' | 'manual'
    chain TEXT NOT NULL DEFAULT '',             -- EVM chain for on-chain legs
    amount_usdc INTEGER NOT NULL,               -- USDC base units (6 decimals)
    tx_hash TEXT NOT NULL DEFAULT '',           -- on-chain tx hash for EVM legs
    deposit_address TEXT NOT NULL DEFAULT '',   -- 1-Click deposit address for intents legs (status poll key)
    topup_id INTEGER,                           -- linked topup for user payments / confidential swaps
    status TEXT NOT NULL DEFAULT 'pending',     -- 'pending' | 'completed' | 'failed'
    note TEXT NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_treasury_ledger_status ON treasury_ledger(status);
CREATE INDEX idx_treasury_ledger_created_at ON treasury_ledger(created_at);

-- +goose Down
DROP TABLE treasury_ledger;
