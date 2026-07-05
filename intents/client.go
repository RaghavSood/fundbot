package intents

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://1click.chaindefuser.com"

// Client is a 1-Click API client for the service-level intents account.
// The account is an EVM key whose lowercase address is the NEAR implicit
// account ID; all intents are signed ERC-191 with that key.
type Client struct {
	apiKey     string
	key        *ecdsa.PrivateKey
	signerID   string
	httpClient *http.Client

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	tokenExpiry  time.Time
}

// NewClient creates a service-account intents client.
func NewClient(apiKey string, key *ecdsa.PrivateKey, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		apiKey:     apiKey,
		key:        key,
		signerID:   SignerID(key),
		httpClient: httpClient,
	}
}

// SignerID returns the service account ID (lowercase EVM address).
func (c *Client) SignerID() string {
	return c.signerID
}

// doJSON performs an HTTP request with a JSON body (may be nil) and decodes
// the JSON response into out (may be nil). auth selects the credential:
// "apikey" (Bearer API key) or "session" (user-session JWT).
func (c *Client) doJSON(ctx context.Context, method, path string, auth string, body, out interface{}) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	switch auth {
	case "apikey":
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	case "session":
		token, err := c.sessionToken(ctx)
		if err != nil {
			return fmt.Errorf("obtaining session token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("intents %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(respBody), 500))
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding %s response: %w", path, err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type authResponse struct {
	AccessToken      string  `json:"accessToken"`
	RefreshToken     string  `json:"refreshToken"`
	ExpiresIn        float64 `json:"expiresIn"`
	RefreshExpiresIn float64 `json:"refreshExpiresIn"`
}

// sessionToken returns a valid user-session JWT, authenticating or
// refreshing as needed. Caller must not hold c.mu.
//
// NOTE: session auth is only required for the confidential-balance and history
// endpoints (/v0/account/*). The versioned timestamped nonce clears the
// endpoint's timestamp validation, but signature verification is not yet
// accepted by the server despite a byte-canonical ERC-191 payload; this path is
// gated behind Confidential Intents being enabled for the API key, so it is
// left best-effort. Callers surface the error rather than failing hard.
func (c *Client) sessionToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-30*time.Second)) {
		return c.accessToken, nil
	}

	// Try refresh first if we have a refresh token
	if c.refreshToken != "" {
		var resp authResponse
		err := c.doJSON(ctx, http.MethodPost, "/v0/auth/refresh", "", map[string]string{
			"refreshToken": c.refreshToken,
		}, &resp)
		if err == nil && resp.AccessToken != "" {
			c.accessToken = resp.AccessToken
			c.tokenExpiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
			return c.accessToken, nil
		}
		// fall through to full authentication
	}

	payload, err := buildAuthPayload(c.signerID)
	if err != nil {
		return "", err
	}
	signed, err := signedMultiPayload(c.key, payload)
	if err != nil {
		return "", err
	}

	var resp authResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v0/auth/authenticate", "", map[string]interface{}{
		"signedData": signed,
	}, &resp); err != nil {
		return "", err
	}

	c.accessToken = resp.AccessToken
	c.refreshToken = resp.RefreshToken
	c.tokenExpiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	return c.accessToken, nil
}

// BalanceEntry is a token balance from the confidential balance source.
type BalanceEntry struct {
	TokenID   string `json:"tokenId"`
	Available string `json:"available"`
	Source    string `json:"source"`
}

// ConfidentialBalances returns the service account's confidential (private)
// balances. tokenIDs optionally filters; empty returns all non-zero balances.
func (c *Client) ConfidentialBalances(ctx context.Context, tokenIDs ...string) ([]BalanceEntry, error) {
	path := "/v0/account/balances"
	if len(tokenIDs) > 0 {
		q := url.Values{}
		q.Set("tokenIds", strings.Join(tokenIDs, ","))
		path += "?" + q.Encode()
	}
	var resp struct {
		Balances []BalanceEntry `json:"balances"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, "session", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Balances, nil
}

// HistoryItem is a single account history entry (public or confidential).
type HistoryItem struct {
	Status             string `json:"status"`
	DepositType        string `json:"depositType"`
	RecipientType      string `json:"recipientType"`
	RefundType         string `json:"refundType"`
	CreatedAt          string `json:"createdAt"`
	DepositAddress     string `json:"depositAddress"`
	OriginAsset        string `json:"originAsset"`
	AmountInFormatted  string `json:"amountInFormatted"`
	AmountInUsd        string `json:"amountInUsd"`
	DestinationAsset   string `json:"destinationAsset"`
	AmountOutFormatted string `json:"amountOutFormatted"`
	AmountOutUsd       string `json:"amountOutUsd"`
	Recipient          string `json:"recipient"`
	RefundReason       string `json:"refundReason"`
}

// History returns paginated account history. Pass an empty cursor for the
// most recent items; the returned prevCursor fetches older items.
func (c *Client) History(ctx context.Context, prevCursor string, limit int) ([]HistoryItem, string, error) {
	q := url.Values{}
	if prevCursor != "" {
		q.Set("prevCursor", prevCursor)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/v0/account/history"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}

	var resp struct {
		Items      []HistoryItem `json:"items"`
		PrevCursor string        `json:"prevCursor"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, "session", nil, &resp); err != nil {
		return nil, "", err
	}
	return resp.Items, resp.PrevCursor, nil
}

// Status returns the execution status for a deposit address
// (KNOWN_DEPOSIT_TX, PENDING_DEPOSIT, INCOMPLETE_DEPOSIT, PROCESSING,
// SUCCESS, REFUNDED, FAILED).
func (c *Client) Status(ctx context.Context, depositAddress string) (string, error) {
	q := url.Values{}
	q.Set("depositAddress", depositAddress)
	var resp struct {
		Status string `json:"status"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v0/status?"+q.Encode(), "apikey", nil, &resp); err != nil {
		return "", err
	}
	return resp.Status, nil
}

// SubmitDepositTx notifies 1-Click of an on-chain deposit tx (best-effort).
func (c *Client) SubmitDepositTx(ctx context.Context, txHash, depositAddress string) error {
	return c.doJSON(ctx, http.MethodPost, "/v0/deposit/submit", "apikey", map[string]string{
		"txHash":         txHash,
		"depositAddress": depositAddress,
	}, nil)
}
