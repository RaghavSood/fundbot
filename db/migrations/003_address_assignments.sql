-- +goose Up
CREATE TABLE address_assignments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    assigned_to_id INTEGER NOT NULL,
    assigned_to_type TEXT NOT NULL CHECK (assigned_to_type IN ('user', 'chat')),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (assigned_to_id, assigned_to_type)
);

-- Seed from existing users and chats so current wallets keep their indices.
INSERT INTO address_assignments (assigned_to_id, assigned_to_type, created_at)
    SELECT id, 'user', created_at FROM users ORDER BY id;
INSERT INTO address_assignments (assigned_to_id, assigned_to_type, created_at)
    SELECT id, 'chat', created_at FROM chats ORDER BY id;

-- +goose Down
DROP TABLE address_assignments;
