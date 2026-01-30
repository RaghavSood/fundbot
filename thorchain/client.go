package thorchain

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type QuoteResponse struct {
	InboundAddress      string       `json:"inbound_address"`
	Router              string       `json:"router"`
	Expiry              int64        `json:"expiry"`
	Memo                string       `json:"memo"`
	ExpectedAmountOut   string       `json:"expected_amount_out"`
	DustThreshold       string       `json:"dust_threshold"`
	RecommendedMinIn    string       `json:"recommended_min_amount_in"`
	RecommendedGasRate  string       `json:"recommended_gas_rate"`
	GasRateUnits        string       `json:"gas_rate_units"`
	Fees                QuoteFees    `json:"fees"`
	OutboundDelayBlocks int64        `json:"outbound_delay_blocks"`
	OutboundDelaySecs   int64        `json:"outbound_delay_seconds"`
	StreamingSwapBlocks int64        `json:"streaming_swap_blocks"`
	MaxStreamingQty     int64        `json:"max_streaming_quantity"`
	Warning             string       `json:"warning"`
	Notes               string       `json:"notes"`
}

type QuoteFees struct {
	Asset        string `json:"asset"`
	Affiliate    string `json:"affiliate"`
	Outbound     string `json:"outbound"`
	Liquidity    string `json:"liquidity"`
	Total        string `json:"total"`
	SlippageBps  int    `json:"slippage_bps"`
	TotalBps     int    `json:"total_bps"`
}

type InboundAddress struct {
	Chain        string `json:"chain"`
	Address      string `json:"address"`
	Router       string `json:"router"`
	Halted       bool   `json:"halted"`
	GasRate      string `json:"gas_rate"`
	GasRateUnits string `json:"gas_rate_units"`
	DustThreshold string `json:"dust_threshold"`
}

type TxStage struct {
	Completed bool `json:"completed"`
}

type TxStatusResponse struct {
	Stages struct {
		InboundObserved            TxStage `json:"inbound_observed"`
		InboundConfirmationCounted TxStage `json:"inbound_confirmation_counted"`
		InboundFinalised           TxStage `json:"inbound_finalised"`
		SwapFinalised              TxStage `json:"swap_finalised"`
		OutboundSigned             TxStage `json:"outbound_signed"`
	} `json:"stages"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	mu         sync.Mutex
	lastReq    time.Time
}

func NewClient() *Client {
	return &Client{
		baseURL: ThornodeBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// rateLimit enforces 1 request per second
func (c *Client) rateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	since := time.Since(c.lastReq)
	if since < time.Second {
		time.Sleep(time.Second - since)
	}
	c.lastReq = time.Now()
}

func (c *Client) GetQuote(ctx context.Context, fromAsset, toAsset, destination string, amount int64) (*QuoteResponse, error) {
	c.rateLimit()

	params := url.Values{}
	params.Set("from_asset", fromAsset)
	params.Set("to_asset", toAsset)
	params.Set("amount", fmt.Sprintf("%d", amount))
	params.Set("destination", destination)
	params.Set("streaming_interval", "1")
	params.Set("streaming_quantity", "0")

	reqURL := fmt.Sprintf("%s/thorchain/quote/swap?%s", c.baseURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting quote: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("quote API returned %d: %s", resp.StatusCode, string(body))
	}

	var quote QuoteResponse
	if err := json.Unmarshal(body, &quote); err != nil {
		return nil, fmt.Errorf("parsing quote: %w", err)
	}

	return &quote, nil
}

func (c *Client) GetInboundAddresses(ctx context.Context) ([]InboundAddress, error) {
	c.rateLimit()

	reqURL := fmt.Sprintf("%s/thorchain/inbound_addresses", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting inbound addresses: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("inbound addresses API returned %d: %s", resp.StatusCode, string(body))
	}

	var addrs []InboundAddress
	if err := json.Unmarshal(body, &addrs); err != nil {
		return nil, fmt.Errorf("parsing inbound addresses: %w", err)
	}

	return addrs, nil
}

func (c *Client) GetTxStatus(ctx context.Context, txHash string) (*TxStatusResponse, error) {
	c.rateLimit()

	// Strip 0x prefix if present
	hash := strings.TrimPrefix(txHash, "0x")

	reqURL := fmt.Sprintf("%s/thorchain/tx/status/%s", c.baseURL, hash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting tx status: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tx status API returned %d: %s", resp.StatusCode, string(body))
	}

	var status TxStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("parsing tx status: %w", err)
	}

	return &status, nil
}
