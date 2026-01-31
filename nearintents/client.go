package nearintents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	oneclick "github.com/defuse-protocol/one-click-sdk-go"
)

// Client wraps the 1click SDK with API key authentication.
type Client struct {
	api    *oneclick.APIClient
	apiKey string
}

// NewClient creates a new Near Intents 1click API client.
func NewClient(apiKey string) *Client {
	cfg := oneclick.NewConfiguration()
	return &Client{
		api:    oneclick.NewAPIClient(cfg),
		apiKey: apiKey,
	}
}

// authCtx returns a context with the bearer token set.
func (c *Client) authCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, oneclick.ContextAccessToken, c.apiKey)
}

// GetQuote requests a swap quote from the 1click API.
func (c *Client) GetQuote(ctx context.Context, req oneclick.QuoteRequest) (*oneclick.QuoteResponse, error) {
	resp, _, err := c.api.OneClickAPI.GetQuote(c.authCtx(ctx)).QuoteRequest(req).Execute()
	if err != nil {
		return nil, fmt.Errorf("nearintents GetQuote: %w", err)
	}
	return resp, nil
}

// SubmitDepositTx notifies 1click of the deposit transaction hash to speed up processing.
func (c *Client) SubmitDepositTx(ctx context.Context, txHash, depositAddress string) error {
	req := *oneclick.NewSubmitDepositTxRequest(txHash, depositAddress)
	_, _, err := c.api.OneClickAPI.SubmitDepositTx(c.authCtx(ctx)).SubmitDepositTxRequest(req).Execute()
	if err != nil {
		return fmt.Errorf("nearintents SubmitDepositTx: %w", err)
	}
	return nil
}

// executionStatusResponse is a minimal struct for parsing the status endpoint response,
// bypassing the SDK's strict model validation which rejects valid API responses.
type executionStatusResponse struct {
	Status string `json:"status"`
}

// GetExecutionStatus checks the status of a swap by deposit address.
// Uses direct HTTP instead of the SDK to avoid deserialization errors from strict model validation.
func (c *Client) GetExecutionStatus(ctx context.Context, depositAddress string) (string, error) {
	url := fmt.Sprintf("https://1click.chaindefuser.com/v0/status?depositAddress=%s", depositAddress)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("nearintents GetExecutionStatus: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("nearintents GetExecutionStatus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nearintents GetExecutionStatus: HTTP %d", resp.StatusCode)
	}

	var result executionStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("nearintents GetExecutionStatus: %w", err)
	}
	return result.Status, nil
}
