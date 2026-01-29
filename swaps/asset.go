package swaps

import (
	"fmt"
	"strings"
)

// Asset represents a blockchain asset in Thorchain notation: CHAIN.SYMBOL or CHAIN.SYMBOL-0xCONTRACT
type Asset struct {
	Chain           string
	Symbol          string
	ContractAddress string // empty for native assets
}

// ParseAsset parses Thorchain asset notation.
// Examples: "BTC.BTC", "ETH.USDC-0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
func ParseAsset(s string) (Asset, error) {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Asset{}, fmt.Errorf("invalid asset notation %q: expected CHAIN.SYMBOL", s)
	}

	chain := strings.ToUpper(parts[0])
	symbolPart := parts[1]

	var symbol, contract string
	if idx := strings.Index(symbolPart, "-"); idx != -1 {
		symbol = strings.ToUpper(symbolPart[:idx])
		contract = symbolPart[idx+1:]
	} else {
		symbol = strings.ToUpper(symbolPart)
	}

	return Asset{
		Chain:           chain,
		Symbol:          symbol,
		ContractAddress: contract,
	}, nil
}

// String returns the asset in Thorchain notation.
func (a Asset) String() string {
	if a.ContractAddress != "" {
		return fmt.Sprintf("%s.%s-%s", a.Chain, a.Symbol, a.ContractAddress)
	}
	return fmt.Sprintf("%s.%s", a.Chain, a.Symbol)
}

// IsNative returns true if the asset is a chain-native asset (no contract address).
func (a Asset) IsNative() bool {
	return a.ContractAddress == ""
}
