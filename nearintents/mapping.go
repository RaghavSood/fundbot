package nearintents

import (
	"github.com/RaghavSood/fundbot/swaps"
)

// assetToTokenID maps Thorchain-notation assets to Near Intents 1click token IDs.
var assetToTokenID = map[string]string{
	// Major L1s
	"BTC.BTC":   "nep141:btc.omft.near",
	"ETH.ETH":   "nep141:eth.omft.near",
	"SOL.SOL":   "nep141:sol.omft.near",
	"AVAX.AVAX": "nep245:v2_1.omni.hot.tg:43114_11111111111111111111",
	"ADA.ADA":   "nep141:cardano.omft.near",
	"TON.TON":   "nep245:v2_1.omni.hot.tg:1117_",
	"TRX.TRX":   "nep141:tron.omft.near",
	"SUI.SUI":   "nep141:sui.omft.near",
	"XRP.XRP":   "nep141:xrp.omft.near",

	// L2s / EVM sidechains
	"BSC.BNB":    "nep245:v2_1.omni.hot.tg:56_11111111111111111111",
	"POLYGON.POL": "nep245:v2_1.omni.hot.tg:137_11111111111111111111",

	// UTXO chains
	"LTC.LTC":   "nep141:ltc.omft.near",
	"BCH.BCH":   "nep141:bch.omft.near",
	"DOGE.DOGE": "nep141:doge.omft.near",
}

// sourceChainTokenID maps RPC chain name to the Near Intents USDC token ID for that chain.
var sourceChainTokenID = map[string]string{
	"avalanche": "nep245:v2_1.omni.hot.tg:43114_3atVJH3r5c4GqiSYmg9fECvjc47o",
	"base":      "nep141:base-0x833589fcd6edb6e08f4c7c32d4f71b54bda02913.omft.near",
}

// AssetToTokenID looks up the Near Intents token ID for a target asset.
func AssetToTokenID(asset swaps.Asset) (string, bool) {
	key := asset.Chain + "." + asset.Symbol
	id, ok := assetToTokenID[key]
	return id, ok
}

// SourceTokenID returns the Near Intents USDC token ID for a source chain.
func SourceTokenID(chain string) (string, bool) {
	id, ok := sourceChainTokenID[chain]
	return id, ok
}

// SupportedSourceChains returns the RPC chain keys that Near Intents can source USDC from.
func SupportedSourceChains() []string {
	chains := make([]string, 0, len(sourceChainTokenID))
	for k := range sourceChainTokenID {
		chains = append(chains, k)
	}
	return chains
}
