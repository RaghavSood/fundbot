-- +goose Up
CREATE TABLE gas_refills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chain TEXT NOT NULL,
    order_uid TEXT NOT NULL,
    wallet_address TEXT NOT NULL,
    sell_amount TEXT NOT NULL,
    buy_amount TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    user_id INTEGER NOT NULL DEFAULT 0,
    chat_id INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE gas_refills;
