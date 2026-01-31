package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const thornodePoolsURL = "https://thornode.ninerealms.com/thorchain/pools"

type tcPool struct {
	Asset  string `json:"asset"`
	Status string `json:"status"`
}

// parsed pool components
type parsedPool struct {
	Raw      string // original asset string
	Chain    string
	Symbol   string
	Contract string // lowercase, empty for native
}

type poolMatcher struct {
	httpClient *http.Client
	cache      *Cache[[]parsedPool]
}

func newPoolMatcher() *poolMatcher {
	return &poolMatcher{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		cache:      NewCache[[]parsedPool](10 * time.Minute),
	}
}

func (pm *poolMatcher) fetchPools(ctx context.Context) ([]parsedPool, error) {
	return pm.cache.GetOrFetch("pools", func() ([]parsedPool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, thornodePoolsURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := pm.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("thorchain pools: HTTP %d", resp.StatusCode)
		}

		var pools []tcPool
		if err := json.NewDecoder(resp.Body).Decode(&pools); err != nil {
			return nil, fmt.Errorf("thorchain pools decode: %w", err)
		}

		var parsed []parsedPool
		for _, p := range pools {
			if p.Status != "Available" {
				continue
			}
			pp := parsePoolAsset(p.Asset)
			if pp.Chain != "" {
				parsed = append(parsed, pp)
			}
		}
		return parsed, nil
	})
}

func parsePoolAsset(asset string) parsedPool {
	parts := strings.SplitN(asset, ".", 2)
	if len(parts) != 2 {
		return parsedPool{}
	}
	chain := parts[0]
	rest := parts[1]

	var symbol, contract string
	if idx := strings.Index(rest, "-"); idx != -1 {
		symbol = rest[:idx]
		contract = strings.ToLower(rest[idx+1:])
	} else {
		symbol = rest
	}

	return parsedPool{
		Raw:      asset,
		Chain:    chain,
		Symbol:   symbol,
		Contract: contract,
	}
}

// matchPool finds a Thorchain pool matching the given chain and contract address.
// For native assets (empty contractAddr), matches by chain+symbol.
func (pm *poolMatcher) matchPool(ctx context.Context, chain, symbol, contractAddr string) (string, bool, error) {
	pools, err := pm.fetchPools(ctx)
	if err != nil {
		return "", false, err
	}

	chainUpper := strings.ToUpper(chain)
	symbolUpper := strings.ToUpper(symbol)
	contractLower := strings.ToLower(contractAddr)

	for _, p := range pools {
		if !strings.EqualFold(p.Chain, chainUpper) {
			continue
		}

		if contractLower != "" && p.Contract != "" {
			// Match by contract address
			if p.Contract == contractLower {
				return p.Raw, true, nil
			}
		} else if contractLower == "" && p.Contract == "" {
			// Native asset: match by symbol
			if strings.EqualFold(p.Symbol, symbolUpper) {
				return p.Raw, true, nil
			}
		}
	}

	return "", false, nil
}
