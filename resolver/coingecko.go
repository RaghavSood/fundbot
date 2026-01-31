package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const coingeckoBase = "https://api.coingecko.com/api/v3"

// platformToChain maps CoinGecko platform names to Thorchain-style chain identifiers.
var platformToChain = map[string]string{
	"ethereum":             "ETH",
	"avalanche":            "AVAX",
	"base":                 "BASE",
	"binance-smart-chain":  "BSC",
	"polygon-pos":          "POLYGON",
	"solana":               "SOL",
	"arbitrum-one":         "ARB",
	"tron":                 "TRON",
	"bitcoin":              "BTC",
	"litecoin":             "LTC",
	"dogecoin":             "DOGE",
	"bitcoin-cash":         "BCH",
	"cosmos":               "GAIA",
	"thorchain":            "THOR",
	"sui":                  "SUI",
	"the-open-network":     "TON",
	"xrp":                  "XRP",
	"polkadot":             "DOT",
	"cardano":              "ADA",
}

type cgSearchResult struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	MarketCapRank *int   `json:"market_cap_rank"`
}

type cgSearchResponse struct {
	Coins []cgSearchResult `json:"coins"`
}

type coingeckoClient struct {
	apiKey      string
	httpClient  *http.Client
	searchCache *Cache[[]cgSearchResult]
	coinCache   *Cache[map[string]string] // coinID → {platform: contractAddr}
}

func newCoingeckoClient(apiKey string) *coingeckoClient {
	return &coingeckoClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		searchCache: NewCache[[]cgSearchResult](1 * time.Hour),
		coinCache:   NewCache[map[string]string](1 * time.Hour),
	}
}

// search finds coins matching the given symbol, returning results sorted by market cap.
func (c *coingeckoClient) search(ctx context.Context, symbol string) ([]cgSearchResult, error) {
	key := strings.ToLower(symbol)
	return c.searchCache.GetOrFetch(key, func() ([]cgSearchResult, error) {
		u := fmt.Sprintf("%s/search?query=%s&x_cg_demo_api_key=%s",
			coingeckoBase, url.QueryEscape(symbol), url.QueryEscape(c.apiKey))

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("coingecko search: HTTP %d", resp.StatusCode)
		}

		var result cgSearchResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("coingecko search decode: %w", err)
		}

		return result.Coins, nil
	})
}

// bestMatch picks the coin with the best (lowest) market cap rank whose symbol matches.
func (c *coingeckoClient) bestMatch(coins []cgSearchResult, symbol string) *cgSearchResult {
	var best *cgSearchResult
	for i := range coins {
		coin := &coins[i]
		if !strings.EqualFold(coin.Symbol, symbol) {
			continue
		}
		if coin.MarketCapRank == nil || *coin.MarketCapRank == 0 {
			if best == nil {
				best = coin
			}
			continue
		}
		if best == nil || best.MarketCapRank == nil || *coin.MarketCapRank < *best.MarketCapRank {
			best = coin
		}
	}
	return best
}

// getPlatforms returns a map of chain ID → contract address for the given CoinGecko coin ID.
func (c *coingeckoClient) getPlatforms(ctx context.Context, coinID string) (map[string]string, error) {
	return c.coinCache.GetOrFetch(coinID, func() (map[string]string, error) {
		u := fmt.Sprintf("%s/coins/%s?localization=false&tickers=false&market_data=false&community_data=false&developer_data=false&x_cg_demo_api_key=%s",
			coingeckoBase, url.PathEscape(coinID), url.QueryEscape(c.apiKey))

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("coingecko coin: HTTP %d", resp.StatusCode)
		}

		var raw struct {
			Platforms map[string]string `json:"platforms"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, fmt.Errorf("coingecko coin decode: %w", err)
		}

		// Convert platform names to chain IDs, skip empty entries.
		result := make(map[string]string)
		for platform, addr := range raw.Platforms {
			if platform == "" || addr == "" {
				continue
			}
			if chain, ok := platformToChain[platform]; ok {
				result[chain] = addr
			}
		}

		return result, nil
	})
}
