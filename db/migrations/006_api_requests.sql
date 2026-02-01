-- +goose Up
CREATE TABLE api_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider TEXT NOT NULL,
    method TEXT NOT NULL,
    url TEXT NOT NULL,
    request_headers TEXT,
    request_body TEXT,
    response_status INTEGER,
    response_headers TEXT,
    response_body TEXT,
    duration_ms INTEGER,
    error TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_api_requests_provider ON api_requests(provider);
CREATE INDEX idx_api_requests_created_at ON api_requests(created_at);

-- +goose Down
DROP TABLE api_requests;
