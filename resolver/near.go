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

// matchToken finds a Near Intents token by symbol (case-insensitive).
// If multiple matches, prefers the one with highest price (proxy for most liquid).
func (nm *nearMatcher) matchToken(ctx context.Context, symbol string) (string, bool, error) {
	tokens, err := nm.fetchTokens(ctx)
	if err != nil {
		return "", false, err
	}

	var best *nearToken
	for i := range tokens {
		t := &tokens[i]
		if !strings.EqualFold(t.Symbol, symbol) {
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
