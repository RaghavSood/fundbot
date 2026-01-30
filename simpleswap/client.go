package simpleswap

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

const baseURL = "https://api.simpleswap.io"

type Client struct {
	apiKey     string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetEstimated returns the estimated output amount for a swap.
func (c *Client) GetEstimated(ctx context.Context, from, to string, amount float64) (string, error) {
	u := fmt.Sprintf("%s/get_estimated?api_key=%s&fixed=false&currency_from=%s&currency_to=%s&amount=%g",
		baseURL, c.apiKey, from, to, amount)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("simpleswap get_estimated: %s: %s", resp.Status, body)
	}

	// Response is a quoted string like "0.00123456"
	var result string
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing estimated response: %w", err)
	}

	return result, nil
}

type Exchange struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	AddressFrom string `json:"address_from"`
	AddressTo   string `json:"address_to"`
	AmountFrom  string `json:"expected_amount"`
	AmountTo    string `json:"amount_to"`
}

// CreateExchange creates a new exchange and returns the exchange details including the deposit address.
func (c *Client) CreateExchange(ctx context.Context, from, to, amount, addressTo, refundAddress string) (*Exchange, error) {
	u := fmt.Sprintf("%s/create_exchange?api_key=%s", baseURL, c.apiKey)

	payload := map[string]interface{}{
		"fixed":          false,
		"currency_from":  from,
		"currency_to":    to,
		"amount":         amount,
		"address_to":     addressTo,
		"extra_id_to":    "",
		"user_refund_address": refundAddress,
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

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
		return nil, fmt.Errorf("simpleswap create_exchange: %s: %s", resp.Status, body)
	}

	var exchange Exchange
	if err := json.Unmarshal(body, &exchange); err != nil {
		return nil, fmt.Errorf("parsing exchange response: %w", err)
	}

	return &exchange, nil
}

// GetExchange retrieves the current status of an exchange.
func (c *Client) GetExchange(ctx context.Context, id string) (*Exchange, error) {
	u := fmt.Sprintf("%s/get_exchange?api_key=%s&id=%s", baseURL, c.apiKey, url.QueryEscape(id))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

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
		return nil, fmt.Errorf("simpleswap get_exchange: %s: %s", resp.Status, body)
	}

	var exchange Exchange
	if err := json.Unmarshal(body, &exchange); err != nil {
		return nil, fmt.Errorf("parsing exchange response: %w", err)
	}

	return &exchange, nil
}
