-- name: InsertAPIRequest :exec
INSERT INTO api_requests (provider, method, url, request_headers, request_body,
    response_status, response_headers, response_body, duration_ms, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
