-- +goose Up
CREATE INDEX idx_api_requests_url ON api_requests(url);
CREATE INDEX idx_api_requests_method ON api_requests(method);
CREATE INDEX idx_api_requests_response_status ON api_requests(response_status);

-- +goose Down
DROP INDEX idx_api_requests_url;
DROP INDEX idx_api_requests_method;
DROP INDEX idx_api_requests_response_status;
