package resolver

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/RaghavSood/fundbot/swaps"
)

// ProviderMatch represents a successful match of a token on a specific provider.
type ProviderMatch struct {
	Provider string // "thorchain", "simpleswap", "nearintents"
	AssetID  string // provider-specific identifier
}

// Resolution holds the result of resolving an unknown asset.
type Resolution struct {
	CoinGeckoID     string
	Name            string // e.g. "Chainlink"
	Symbol          string // e.g. "LINK"
	ContractAddress string // primary contract address for display
	Providers       []ProviderMatch
}

// Resolver resolves unknown assets by querying CoinGecko and matching against provider APIs.
type Resolver struct {
	cg    *coingeckoClient
	pools *poolMatcher
	near  *nearMatcher
	// simpleswapLookup checks the SimpleSwap static mapping.
	simpleswapLookup func(key string) (string, bool)
	// houdiniLookup checks the Houdini static mapping.
	houdiniLookup func(key string) (string, bool)
}

// New creates a new Resolver.
func New(cgAPIKey string, simpleswapLookup func(key string) (string, bool), houdiniLookup func(key string) (string, bool)) *Resolver {
	return &Resolver{
		cg:               newCoingeckoClient(cgAPIKey),
		pools:            newPoolMatcher(),
		near:             newNearMatcher(),
		simpleswapLookup: simpleswapLookup,
		houdiniLookup:    houdiniLookup,
	}
}

// Resolve attempts to identify and match an unknown asset across providers.
func (r *Resolver) Resolve(ctx context.Context, asset swaps.Asset) (*Resolution, error) {
	// Search CoinGecko for the symbol.
	coins, err := r.cg.search(ctx, asset.Symbol)
	if err != nil {
		return nil, fmt.Errorf("coingecko search: %w", err)
	}

	best := r.cg.bestMatch(coins, asset.Symbol)
	if best == nil {
		return nil, fmt.Errorf("no CoinGecko result for symbol %q", asset.Symbol)
	}

	// Get platform/contract info.
	platforms, err := r.cg.getPlatforms(ctx, best.ID)
	if err != nil {
		return nil, fmt.Errorf("coingecko platforms: %w", err)
	}

	res := &Resolution{
		CoinGeckoID: best.ID,
		Name:        best.Name,
		Symbol:      strings.ToUpper(best.Symbol),
	}

	// Try to find a display contract address for the user's specified chain.
	if addr, ok := platforms[strings.ToUpper(asset.Chain)]; ok {
		res.ContractAddress = addr
	}

	// --- Thorchain matching ---
	r.matchThorchain(ctx, asset, platforms, res)

	// --- Near Intents matching ---
	r.matchNearIntents(ctx, asset, res)

	// --- SimpleSwap matching ---
	r.matchSimpleSwap(asset, res)

	// --- Houdini matching ---
	r.matchHoudini(asset, res)

	if len(res.Providers) == 0 {
		return nil, fmt.Errorf("token %s (%s) found on CoinGecko but not supported by any provider", res.Name, res.Symbol)
	}

	return res, nil
}

func (r *Resolver) matchThorchain(ctx context.Context, asset swaps.Asset, platforms map[string]string, res *Resolution) {
	// If the user specified a contract address, try matching it directly.
	if asset.ContractAddress != "" {
		poolAsset, found, err := r.pools.matchPool(ctx, asset.Chain, asset.Symbol, asset.ContractAddress)
		if err != nil {
			log.Printf("resolver: thorchain pool match error: %v", err)
		} else if found {
			res.Providers = append(res.Providers, ProviderMatch{Provider: "thorchain", AssetID: poolAsset})
			return
		}
	}

	// Try matching CoinGecko platform contract addresses against Thorchain pools.
	for chain, addr := range platforms {
		poolAsset, found, err := r.pools.matchPool(ctx, chain, asset.Symbol, addr)
		if err != nil {
			log.Printf("resolver: thorchain pool match error for %s: %v", chain, err)
			continue
		}
		if found {
			res.Providers = append(res.Providers, ProviderMatch{Provider: "thorchain", AssetID: poolAsset})
			if res.ContractAddress == "" {
				res.ContractAddress = addr
			}
			return
		}
	}

	// For native assets, try direct chain.symbol match (e.g. BTC.BTC).
	if len(platforms) == 0 || asset.ContractAddress == "" {
		poolAsset, found, err := r.pools.matchPool(ctx, asset.Chain, asset.Symbol, "")
		if err != nil {
			log.Printf("resolver: thorchain native match error: %v", err)
		} else if found {
			res.Providers = append(res.Providers, ProviderMatch{Provider: "thorchain", AssetID: poolAsset})
		}
	}
}

func (r *Resolver) matchNearIntents(ctx context.Context, asset swaps.Asset, res *Resolution) {
	tokenID, found, err := r.near.matchToken(ctx, asset.Chain, asset.Symbol)
	if err != nil {
		log.Printf("resolver: near intents match error: %v", err)
		return
	}
	if found {
		res.Providers = append(res.Providers, ProviderMatch{Provider: "nearintents", AssetID: tokenID})
	}
}

func (r *Resolver) matchSimpleSwap(asset swaps.Asset, res *Resolution) {
	if r.simpleswapLookup == nil {
		return
	}

	// Try using the Thorchain asset notation if we matched Thorchain.
	for _, pm := range res.Providers {
		if pm.Provider == "thorchain" {
			parts := strings.SplitN(pm.AssetID, ".", 2)
			if len(parts) == 2 {
				symbolPart := parts[1]
				if idx := strings.Index(symbolPart, "-"); idx != -1 {
					symbolPart = symbolPart[:idx]
				}
				key := parts[0] + "." + symbolPart
				if sym, ok := r.simpleswapLookup(strings.ToUpper(key)); ok {
					res.Providers = append(res.Providers, ProviderMatch{Provider: "simpleswap", AssetID: sym})
					return
				}
			}
		}
	}

	// Fallback: try the original user-provided chain.symbol.
	key := strings.ToUpper(asset.Chain + "." + asset.Symbol)
	if sym, ok := r.simpleswapLookup(key); ok {
		res.Providers = append(res.Providers, ProviderMatch{Provider: "simpleswap", AssetID: sym})
	}
}

func (r *Resolver) matchHoudini(asset swaps.Asset, res *Resolution) {
	if r.houdiniLookup == nil {
		return
	}

	// Try using the Thorchain asset notation if we matched Thorchain.
	for _, pm := range res.Providers {
		if pm.Provider == "thorchain" {
			parts := strings.SplitN(pm.AssetID, ".", 2)
			if len(parts) == 2 {
				symbolPart := parts[1]
				if idx := strings.Index(symbolPart, "-"); idx != -1 {
					symbolPart = symbolPart[:idx]
				}
				key := parts[0] + "." + symbolPart
				if sym, ok := r.houdiniLookup(strings.ToUpper(key)); ok {
					res.Providers = append(res.Providers, ProviderMatch{Provider: "houdini", AssetID: sym})
					return
				}
			}
		}
	}

	// Fallback: try the original user-provided chain.symbol.
	key := strings.ToUpper(asset.Chain + "." + asset.Symbol)
	if sym, ok := r.houdiniLookup(key); ok {
		res.Providers = append(res.Providers, ProviderMatch{Provider: "houdini", AssetID: sym})
	}
}

// ToHints converts a Resolution into ResolvedHints for the swap providers.
func (res *Resolution) ToHints() *swaps.ResolvedHints {
	hints := &swaps.ResolvedHints{}
	for _, pm := range res.Providers {
		switch pm.Provider {
		case "thorchain":
			hints.ThorchainAsset = pm.AssetID
		case "simpleswap":
			hints.SimpleSwapSymbol = pm.AssetID
		case "nearintents":
			hints.NearIntentsTokenID = pm.AssetID
		case "houdini":
			hints.HoudiniSymbol = pm.AssetID
		}
	}
	return hints
}
