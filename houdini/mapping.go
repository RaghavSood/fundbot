package houdini

import (
	"github.com/RaghavSood/fundbot/swaps"
)

// assetToSymbol maps our Asset notation (CHAIN.SYMBOL) to Houdini token ID.
var assetToSymbol = map[string]string{
	// Major L1s
	"BTC.BTC":   "BTC",
	"ETH.ETH":   "ETH",
	"SOL.SOL":   "SOL",
	"AVAX.AVAX": "AVAXC", // C-chain
	"DOT.DOT":   "DOT",
	"ADA.ADA":   "ADA",
	"TON.TON":   "TON",
	"TRX.TRX":   "TRX",
	"SUI.SUI":   "SUI",

	// L2s / EVM sidechains
	"BASE.ETH": "ETHBASE",
	"ARB.ETH":  "ETHARB",
	"BSC.BNB":  "BNB",

	// Cosmos ecosystem
	"GAIA.ATOM": "ATOM",
	"THOR.RUNE": "RUNE",
	"SEI.SEI":   "SEI",

	// UTXO chains
	"LTC.LTC":   "LTC",
	"BCH.BCH":   "BCH",
	"DOGE.DOGE": "DOGE",
	"DASH.DASH": "DASH",
	"ZEC.ZEC":   "ZEC",
}

// sourceChainSymbol maps our RPC chain name to the Houdini USDC token ID for that chain.
var sourceChainSymbol = map[string]string{
	"avalanche": "USDCAVAXC",
	"base":      "USDCBASE",
}

// AssetToSymbol looks up the Houdini token ID for a target asset.
func AssetToSymbol(asset swaps.Asset) (string, bool) {
	key := asset.Chain + "." + asset.Symbol
	sym, ok := assetToSymbol[key]
	return sym, ok
}

// LookupSymbol checks the static mapping by a CHAIN.SYMBOL key string (uppercase).
func LookupSymbol(key string) (string, bool) {
	sym, ok := assetToSymbol[key]
	return sym, ok
}

// SourceSymbol returns the Houdini USDC token ID for a source chain.
func SourceSymbol(chain string) (string, bool) {
	sym, ok := sourceChainSymbol[chain]
	return sym, ok
}

// SupportedSourceChains returns the RPC chain keys that Houdini can source USDC from.
func SupportedSourceChains() []string {
	chains := make([]string, 0, len(sourceChainSymbol))
	for k := range sourceChainSymbol {
		chains = append(chains, k)
	}
	return chains
}
