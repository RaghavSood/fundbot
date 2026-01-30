package simpleswap

import (
	"github.com/RaghavSood/fundbot/swaps"
)

// assetToSymbol maps our Asset notation (CHAIN.SYMBOL) to SimpleSwap currency symbol.
// This is a curated list of assets we support.
var assetToSymbol = map[string]string{
	"BTC.BTC":  "btc",
	"ETH.ETH":  "eth",
	"BASE.ETH": "ethbase",
	"SOL.SOL":  "sol",
	"AVAX.AVAX": "avaxc",
	"ARB.ETH":  "etharb",
	"LTC.LTC":  "ltc",
	"DOGE.DOGE": "doge",
	"BCH.BCH":  "bch",
}

// sourceChainSymbol maps our RPC chain name to the SimpleSwap USDC symbol for that chain.
var sourceChainSymbol = map[string]string{
	"avalanche": "usdcavaxc",
	"base":      "usdcbase",
}

// AssetToSymbol looks up the SimpleSwap symbol for a target asset.
func AssetToSymbol(asset swaps.Asset) (string, bool) {
	key := asset.Chain + "." + asset.Symbol
	sym, ok := assetToSymbol[key]
	return sym, ok
}

// SourceSymbol returns the SimpleSwap USDC symbol for a source chain.
func SourceSymbol(chain string) (string, bool) {
	sym, ok := sourceChainSymbol[chain]
	return sym, ok
}

// SupportedSourceChains returns the RPC chain keys that SimpleSwap can source USDC from.
func SupportedSourceChains() []string {
	chains := make([]string, 0, len(sourceChainSymbol))
	for k := range sourceChainSymbol {
		chains = append(chains, k)
	}
	return chains
}
