package simpleswap

import (
	"github.com/RaghavSood/fundbot/swaps"
)

// assetToSymbol maps our Asset notation (CHAIN.SYMBOL) to SimpleSwap currency symbol.
// This is a curated list of assets we support.
// Note: kuji (Kujira) is not available on SimpleSwap.
// Note: cro on SimpleSwap is an ERC20 token (ETH network), not native Cronos chain.
var assetToSymbol = map[string]string{
	// Major L1s
	"BTC.BTC":   "btc",
	"ETH.ETH":   "eth",
	"SOL.SOL":   "sol",
	"AVAX.AVAX": "avaxc", // C-chain, NOT X-chain
	"DOT.DOT":   "dot",
	"ADA.ADA":   "ada",
	"TON.TON":   "ton",
	"TRX.TRX":   "trx",
	"SUI.SUI":   "sui",

	// L2s / EVM sidechains
	"BASE.ETH": "ethbase",
	"ARB.ETH":  "etharb",
	"BSC.BNB":  "bnb-bsc",
	"POLYGON.POL": "pol",

	// Cosmos ecosystem
	"GAIA.ATOM":  "atom",
	"OSMO.OSMO":  "osmo",
	"DYDX.DYDX":  "dydxmain",
	"SEI.SEI":    "sei",
	"AKASH.AKT":  "akt",
	"NOBLE.USDC": "usdcnoble",
	"LUNA.LUNA":   "luna",
	"LUNC.LUNC":  "lunc",
	"THOR.RUNE":  "rune",

	// UTXO chains
	"LTC.LTC":   "ltc",
	"BCH.BCH":   "bch",
	"DOGE.DOGE": "doge",
	"DASH.DASH": "dash",
	"ZEC.ZEC":   "zec",

	// Other
	"HYPE.HYPE": "hype",
	"CRO.CRO":   "cro", // ERC20 on ETH, not native Cronos
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
