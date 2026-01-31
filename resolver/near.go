package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const nearTokensURL = "https://1click.chaindefuser.com/v0/tokens"

// chainToNearBlockchain maps our uppercase chain IDs to Near Intents blockchain field values.
var chainToNearBlockchain = map[string]string{
	"ETH":     "eth",
	"BASE":    "base",
	"AVAX":    "avax",
	"BSC":     "bsc",
	"POLYGON": "pol",
	"ARB":     "arb",
	"SOL":     "sol",
	"BTC":     "btc",
	"LTC":     "ltc",
	"DOGE":    "doge",
	"BCH":     "bch",
	"TRON":    "tron",
	"TON":     "ton",
	"SUI":     "sui",
	"GAIA":    "near",
}

type nearToken struct {
	AssetID         string  `json:"assetId"`
	Symbol          string  `json:"symbol"`
	Blockchain      string  `json:"blockchain"`
	ContractAddress string  `json:"contractAddress"`
	Decimals        int     `json:"decimals"`
	Price           float64 `json:"price"`
}

type nearMatcher struct {
	httpClient *http.Client
	cache      *Cache[[]nearToken]
}

func newNearMatcher() *nearMatcher {
	return &nearMatcher{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		cache:      NewCache[[]nearToken](10 * time.Minute),
	}
}

func (nm *nearMatcher) fetchTokens(ctx context.Context) ([]nearToken, error) {
	return nm.cache.GetOrFetch("tokens", func() ([]nearToken, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nearTokensURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := nm.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("near tokens: HTTP %d", resp.StatusCode)
		}

		var tokens []nearToken
		if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
			return nil, fmt.Errorf("near tokens decode: %w", err)
		}
		return tokens, nil
	})
}

// matchToken finds a Near Intents token by symbol and chain (both case-insensitive).
// chain is our uppercase chain ID (e.g. "BASE", "ETH"). If chain is empty, picks by highest price.
func (nm *nearMatcher) matchToken(ctx context.Context, chain, symbol string) (string, bool, error) {
	tokens, err := nm.fetchTokens(ctx)
	if err != nil {
		return "", false, err
	}

	wantBlockchain := chainToNearBlockchain[strings.ToUpper(chain)]

	var best *nearToken
	for i := range tokens {
		t := &tokens[i]
		if !strings.EqualFold(t.Symbol, symbol) {
			continue
		}
		// If chain specified, filter by blockchain.
		if wantBlockchain != "" && !strings.EqualFold(t.Blockchain, wantBlockchain) {
			continue
		}
		if best == nil || t.Price > best.Price {
			best = t
		}
	}

	if best != nil {
		return best.AssetID, true, nil
	}
	return "", false, nil
}
