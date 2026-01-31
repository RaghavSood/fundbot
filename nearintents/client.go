package nearintents

import (
	"context"
	"fmt"

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

// GetExecutionStatus checks the status of a swap by deposit address.
func (c *Client) GetExecutionStatus(ctx context.Context, depositAddress string) (*oneclick.GetExecutionStatusResponse, error) {
	resp, _, err := c.api.OneClickAPI.GetExecutionStatus(c.authCtx(ctx)).DepositAddress(depositAddress).Execute()
	if err != nil {
		return nil, fmt.Errorf("nearintents GetExecutionStatus: %w", err)
	}
	return resp, nil
}
