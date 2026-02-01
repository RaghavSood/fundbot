package houdini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://api-partner.houdiniswap.com"

type Client struct {
	apiKey     string
	apiSecret  string
	httpClient *http.Client
}

func NewClient(apiKey, apiSecret string) *Client {
	return &Client{
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) authHeader() string {
	return c.apiKey + ":" + c.apiSecret
}

// QuoteResponse represents the response from GET /quote.
type QuoteResponse struct {
	AmountOut    float64 `json:"amountOut"`
	AmountIn     float64 `json:"amountIn"`
	AmountOutUsd float64 `json:"amountOutUsd"`
	QuoteID      string  `json:"quoteId"`
	InQuoteID    string  `json:"inQuoteId"`
	OutQuoteID   string  `json:"outQuoteId"`
	Min          float64 `json:"min"`
	Max          float64 `json:"max"`
	Duration     int     `json:"duration"`
	SwapName     string  `json:"swapName"`
}

// ExchangeResponse represents the response from POST /exchange.
type ExchangeResponse struct {
	ID              string  `json:"id"`
	HoudiniID       string  `json:"houdiniId"`
	SenderAddress   string  `json:"senderAddress"`
	ReceiverAddress string  `json:"receiverAddress"`
	Status          int     `json:"status"`
	InAmount        float64 `json:"inAmount"`
	OutAmount       float64 `json:"outAmount"`
	InSymbol        string  `json:"inSymbol"`
	OutSymbol       string  `json:"outSymbol"`
	Expires         string  `json:"expires"`
}

// StatusResponse represents the response from GET /status.
type StatusResponse struct {
	HoudiniID string `json:"houdiniId"`
	Status    int    `json:"status"`
	InStatus  int    `json:"inStatus"`
	HashURL   string `json:"hashUrl"`
}

// GetMinMax returns the [min, max] amounts (in source token units) for a pair.
func (c *Client) GetMinMax(ctx context.Context, from, to string, anonymous bool) (min, max float64, err error) {
	u := fmt.Sprintf("%s/getMinMax?from=%s&to=%s&anonymous=%t&cexOnly=true",
		baseURL, url.QueryEscape(from), url.QueryEscape(to), anonymous)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("houdini getMinMax: %s: %s", resp.Status, body)
	}

	var result [2]float64
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, fmt.Errorf("parsing getMinMax response: %w", err)
	}

	return result[0], result[1], nil
}

// GetQuote requests a price quote for a swap.
// It first tries CEX-only routes, falling back to all routes if no CEX quote is available.
func (c *Client) GetQuote(ctx context.Context, from, to string, amount float64) (*QuoteResponse, error) {
	// Try CEX-only first
	quote, err := c.getQuote(ctx, from, to, amount, true)
	if err != nil {
		// Fall back to all routes (includes "no wallet connect" / NWC)
		return c.getQuote(ctx, from, to, amount, false)
	}
	return quote, nil
}

func (c *Client) getQuote(ctx context.Context, from, to string, amount float64, cexOnly bool) (*QuoteResponse, error) {
	u := fmt.Sprintf("%s/quote?amount=%g&from=%s&to=%s&anonymous=false&cexOnly=%t",
		baseURL, amount, url.QueryEscape(from), url.QueryEscape(to), cexOnly)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("houdini quote: %s: %s", resp.Status, body)
	}

	var result QuoteResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing quote response: %w", err)
	}

	return &result, nil
}

// GetQuoteXMR requests a quote using anonymous XMR routing.
func (c *Client) GetQuoteXMR(ctx context.Context, from, to string, amount float64) (*QuoteResponse, error) {
	u := fmt.Sprintf("%s/quote?amount=%g&from=%s&to=%s&anonymous=true&useXmr=true&cexOnly=true",
		baseURL, amount, url.QueryEscape(from), url.QueryEscape(to))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("houdini xmr quote: %s: %s", resp.Status, body)
	}

	var result QuoteResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing xmr quote response: %w", err)
	}

	return &result, nil
}

// CreateExchangeXMR initiates an anonymous XMR-routed swap.
func (c *Client) CreateExchangeXMR(ctx context.Context, from, to string, amount float64, addressTo, inQuoteID, outQuoteID string) (*ExchangeResponse, error) {
	payload := map[string]interface{}{
		"amount":     amount,
		"from":       from,
		"to":         to,
		"addressTo":  addressTo,
		"anonymous":  false,
		"inQuoteId":  inQuoteID,
		"outQuoteId": outQuoteID,
		"ip":         "103.158.32.232",
		"userAgent":  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36",
		"timezone":   "UTC",
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/exchange", strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("houdini xmr exchange: %s: %s", resp.Status, body)
	}

	var exchange ExchangeResponse
	if err := json.Unmarshal(body, &exchange); err != nil {
		return nil, fmt.Errorf("parsing xmr exchange response: %w", err)
	}

	return &exchange, nil
}

// CreateExchange initiates a swap and returns the exchange details including the deposit address.
func (c *Client) CreateExchange(ctx context.Context, from, to string, amount float64, addressTo, quoteID string) (*ExchangeResponse, error) {
	payload := map[string]interface{}{
		"amount":    amount,
		"from":      from,
		"to":        to,
		"addressTo": addressTo,
		"anonymous": false,
		"inQuoteId": quoteID,
		"ip":        "103.158.32.232",
		"userAgent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36",
		"timezone":  "UTC",
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/exchange", strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("houdini exchange: %s: %s", resp.Status, body)
	}

	var exchange ExchangeResponse
	if err := json.Unmarshal(body, &exchange); err != nil {
		return nil, fmt.Errorf("parsing exchange response: %w", err)
	}

	return &exchange, nil
}

// GetStatus retrieves the current status of an exchange by its Houdini ID.
func (c *Client) GetStatus(ctx context.Context, houdiniID string) (*StatusResponse, error) {
	u := fmt.Sprintf("%s/status?id=%s", baseURL, url.QueryEscape(houdiniID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("houdini status: %s: %s", resp.Status, body)
	}

	var status StatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("parsing status response: %w", err)
	}

	return &status, nil
}
