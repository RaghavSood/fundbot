package apilog

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/RaghavSood/fundbot/db"
)

const maxBodySize = 64 * 1024 // 64KB

// Transport is an http.RoundTripper that logs all requests and responses to the database.
type Transport struct {
	inner    http.RoundTripper
	provider string
	store    *db.Store
}

func NewHTTPClient(provider string, store *db.Store) *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &Transport{
			inner:    http.DefaultTransport,
			provider: provider,
			store:    store,
		},
	}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture request body
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	reqHeaders := headerString(req.Header)

	start := time.Now()
	resp, err := t.inner.RoundTrip(req)
	duration := time.Since(start).Milliseconds()

	params := db.InsertAPIRequestParams{
		Provider:       t.provider,
		Method:         req.Method,
		Url:            req.URL.String(),
		RequestHeaders: toNullString(reqHeaders),
		RequestBody:    toNullString(truncate(string(reqBody))),
		DurationMs:     sql.NullInt64{Int64: duration, Valid: true},
	}

	if err != nil {
		params.Error = toNullString(err.Error())
	} else {
		// Capture response body
		var respBody []byte
		if resp.Body != nil {
			respBody, _ = io.ReadAll(resp.Body)
			resp.Body = io.NopCloser(bytes.NewReader(respBody))
		}
		params.ResponseStatus = sql.NullInt64{Int64: int64(resp.StatusCode), Valid: true}
		params.ResponseHeaders = toNullString(headerString(resp.Header))
		params.ResponseBody = toNullString(truncate(string(respBody)))
	}

	// Insert asynchronously so we don't slow down the request
	go func() {
		if dbErr := t.store.InsertAPIRequest(context.Background(), params); dbErr != nil {
			log.Printf("apilog: failed to log %s %s: %v", params.Method, params.Url, dbErr)
		}
	}()

	return resp, err
}

func headerString(h http.Header) string {
	var buf bytes.Buffer
	h.Write(&buf)
	return buf.String()
}

func truncate(s string) string {
	if len(s) > maxBodySize {
		return s[:maxBodySize] + "...[truncated]"
	}
	return s
}

func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
