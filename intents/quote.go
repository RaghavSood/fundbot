package intents

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Deposit/recipient/refund type constants for 1-Click quotes.
const (
	TypeOriginChain         = "ORIGIN_CHAIN"
	TypeDestinationChain    = "DESTINATION_CHAIN"
	TypeIntents             = "INTENTS"
	TypeConfidentialIntents = "CONFIDENTIAL_INTENTS"
)

// QuoteParams describes a 1-Click quote request. Amount is in base units of
// the origin asset.
type QuoteParams struct {
	Dry              bool
	OriginAsset      string
	DestinationAsset string
	Amount           string
	DepositType      string
	Recipient        string
	RecipientType    string
	RefundTo         string
	RefundType       string
	Deadline         time.Time
	SlippageBps      int
}

// QuoteResult is the subset of the 1-Click quote response we use.
type QuoteResult struct {
	CorrelationID  string
	DepositAddress string
	DepositMemo    string
	AmountIn       string
	AmountOut      string
	AmountOutFmt   string
	AmountOutUsd   string
	MinAmountOut   string
	TimeEstimate   float64
	Deadline       string
}

type quoteResponseBody struct {
	CorrelationID string `json:"correlationId"`
	Quote         struct {
		DepositAddress     string  `json:"depositAddress"`
		DepositMemo        string  `json:"depositMemo"`
		AmountIn           string  `json:"amountIn"`
		AmountOut          string  `json:"amountOut"`
		AmountOutFormatted string  `json:"amountOutFormatted"`
		AmountOutUsd       string  `json:"amountOutUsd"`
		MinAmountOut       string  `json:"minAmountOut"`
		TimeEstimate       float64 `json:"timeEstimate"`
		Deadline           string  `json:"deadline"`
	} `json:"quote"`
}

// Quote requests a swap quote. Uses raw HTTP (not the SDK) because the SDK
// models predate the CONFIDENTIAL_INTENTS enums and the intent endpoints.
func (c *Client) Quote(ctx context.Context, p QuoteParams) (*QuoteResult, error) {
	if p.SlippageBps == 0 {
		p.SlippageBps = 100
	}
	if p.Deadline.IsZero() {
		p.Deadline = time.Now().Add(60 * time.Minute)
	}

	req := map[string]interface{}{
		"dry":               p.Dry,
		"swapType":          "EXACT_INPUT",
		"slippageTolerance": p.SlippageBps,
		"originAsset":       p.OriginAsset,
		"depositType":       p.DepositType,
		"destinationAsset":  p.DestinationAsset,
		"amount":            p.Amount,
		"refundTo":          p.RefundTo,
		"refundType":        p.RefundType,
		"recipient":         p.Recipient,
		"recipientType":     p.RecipientType,
		"deadline":          p.Deadline.UTC().Format(time.RFC3339),
		"depositMode":       "SIMPLE",
	}

	var resp quoteResponseBody
	if err := c.doJSON(ctx, http.MethodPost, "/v0/quote", "apikey", req, &resp); err != nil {
		return nil, err
	}

	return &QuoteResult{
		CorrelationID:  resp.CorrelationID,
		DepositAddress: resp.Quote.DepositAddress,
		DepositMemo:    resp.Quote.DepositMemo,
		AmountIn:       resp.Quote.AmountIn,
		AmountOut:      resp.Quote.AmountOut,
		AmountOutFmt:   resp.Quote.AmountOutFormatted,
		AmountOutUsd:   resp.Quote.AmountOutUsd,
		MinAmountOut:   resp.Quote.MinAmountOut,
		TimeEstimate:   resp.Quote.TimeEstimate,
		Deadline:       resp.Quote.Deadline,
	}, nil
}

// GenerateIntent asks 1-Click to build the unsigned intent payload that
// funds the quote identified by depositAddress from the service account.
func (c *Client) GenerateIntent(ctx context.Context, depositAddress string) (string, error) {
	req := map[string]string{
		"type":           "swap_transfer",
		"standard":       "erc191",
		"signerId":       c.signerID,
		"depositAddress": depositAddress,
	}
	var resp struct {
		Intent struct {
			Standard string `json:"standard"`
			Payload  string `json:"payload"`
		} `json:"intent"`
		CorrelationID string `json:"correlationId"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v0/generate-intent", "apikey", req, &resp); err != nil {
		return "", err
	}
	if resp.Intent.Standard != "erc191" {
		return "", fmt.Errorf("generate-intent returned unexpected standard %q", resp.Intent.Standard)
	}
	if resp.Intent.Payload == "" {
		return "", fmt.Errorf("generate-intent returned empty payload")
	}
	return resp.Intent.Payload, nil
}

// SubmitIntent signs the payload with the service key and submits it.
// Returns the intent hash reported by the API (may be empty).
func (c *Client) SubmitIntent(ctx context.Context, payload string) (string, error) {
	signed, err := signedMultiPayload(c.key, payload)
	if err != nil {
		return "", err
	}
	req := map[string]interface{}{
		"type":       "swap_transfer",
		"signedData": signed,
	}
	var resp struct {
		IntentHash string `json:"intentHash"`
		Hash       string `json:"hash"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v0/submit-intent", "apikey", req, &resp); err != nil {
		return "", err
	}
	if resp.IntentHash != "" {
		return resp.IntentHash, nil
	}
	return resp.Hash, nil
}

// ExecuteFromBalance funds a previously requested (non-dry) quote from the
// service account balance: generate intent -> sign -> submit.
func (c *Client) ExecuteFromBalance(ctx context.Context, depositAddress string) (string, error) {
	payload, err := c.GenerateIntent(ctx, depositAddress)
	if err != nil {
		return "", fmt.Errorf("generating intent: %w", err)
	}
	hash, err := c.SubmitIntent(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("submitting intent: %w", err)
	}
	return hash, nil
}
