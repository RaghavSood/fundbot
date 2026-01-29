-- +goose Up
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    telegram_id INTEGER UNIQUE NOT NULL,
    username TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE quotes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL DEFAULT 'fast',
    provider TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    from_asset TEXT NOT NULL,
    from_chain TEXT NOT NULL,
    to_asset TEXT NOT NULL,
    destination TEXT NOT NULL,
    input_amount_usd REAL NOT NULL,
    input_amount TEXT NOT NULL,
    expected_output TEXT NOT NULL,
    memo TEXT NOT NULL,
    router TEXT NOT NULL,
    vault_address TEXT NOT NULL,
    expiry INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE topups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    short_id TEXT UNIQUE NOT NULL,
    type TEXT NOT NULL DEFAULT 'fast',
    quote_id INTEGER NOT NULL REFERENCES quotes(id),
    user_id INTEGER NOT NULL,
    provider TEXT NOT NULL,
    from_chain TEXT NOT NULL,
    tx_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE topups;
DROP TABLE quotes;
DROP TABLE users;
