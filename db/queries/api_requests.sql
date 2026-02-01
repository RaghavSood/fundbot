-- name: InsertAPIRequest :exec
INSERT INTO api_requests (provider, method, url, request_headers, request_body,
    response_status, response_headers, response_body, duration_ms, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SearchAPIRequests :many
SELECT id, provider, method, url, request_headers, request_body,
       response_status, response_headers, response_body, duration_ms, error, created_at
FROM api_requests
WHERE CASE WHEN @search = '' THEN 1 ELSE (
    provider LIKE '%' || @search || '%'
    OR method LIKE '%' || @search || '%'
    OR url LIKE '%' || @search || '%'
    OR COALESCE(request_headers, '') LIKE '%' || @search || '%'
    OR COALESCE(request_body, '') LIKE '%' || @search || '%'
    OR CAST(COALESCE(response_status, 0) AS TEXT) LIKE '%' || @search || '%'
    OR COALESCE(response_headers, '') LIKE '%' || @search || '%'
    OR COALESCE(response_body, '') LIKE '%' || @search || '%'
    OR COALESCE(error, '') LIKE '%' || @search || '%'
) END
ORDER BY created_at DESC LIMIT @limit OFFSET @offset;

-- name: CountAPIRequests :one
SELECT COUNT(*) FROM api_requests
WHERE CASE WHEN @search = '' THEN 1 ELSE (
    provider LIKE '%' || @search || '%'
    OR method LIKE '%' || @search || '%'
    OR url LIKE '%' || @search || '%'
    OR COALESCE(request_headers, '') LIKE '%' || @search || '%'
    OR COALESCE(request_body, '') LIKE '%' || @search || '%'
    OR CAST(COALESCE(response_status, 0) AS TEXT) LIKE '%' || @search || '%'
    OR COALESCE(response_headers, '') LIKE '%' || @search || '%'
    OR COALESCE(response_body, '') LIKE '%' || @search || '%'
    OR COALESCE(error, '') LIKE '%' || @search || '%'
) END;

-- name: GetAPIRequest :one
SELECT id, provider, method, url, request_headers, request_body,
       response_status, response_headers, response_body, duration_ms, error, created_at
FROM api_requests WHERE id = ?;
