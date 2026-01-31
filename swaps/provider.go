package swaps

import (
	"context"
	"crypto/ecdsa"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// Quote represents a swap quote from a provider.
type Quote struct {
	Provider         string
	FromAsset        Asset
	ToAsset          Asset
	FromChain        string  // RPC key: "avalanche" or "base"
	InputAmountUSD   float64
	InputAmount      *big.Int // in source token smallest unit (e.g. 6 decimals for USDC)
	ExpectedOutput   string   // human-readable output amount
	ExpectedOutputRaw *big.Int // in target asset smallest unit
	Memo             string
	Router           string // router contract address
	VaultAddress     string // inbound/vault address
	Expiry           int64  // unix timestamp
	ExtraData        map[string]interface{}
}

// ExecuteResult holds the result of executing a swap.
type ExecuteResult struct {
	TxHash     string
	ExternalID string // provider-specific ID (e.g. SimpleSwap exchange ID)
}

// RoutingHint controls provider selection for a quote request.
type RoutingHint struct {
	Type  string // "" (no hint), "provider", or "category"
	Value string // provider name or category ("dex", "private")
}

// Provider is the interface that swap providers must implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "thorchain").
	Name() string

	// Category returns the provider category: "dex" or "private".
	Category() string

	// Quote returns quotes for swapping usdAmount worth of stablecoins to toAsset,
	// one per supported source chain. The destination is the recipient address on the target chain.
	// sender is the EVM address that will fund the swap (used to check USDC balances).
	Quote(ctx context.Context, toAsset Asset, usdAmount float64, destination string, sender common.Address) ([]Quote, error)

	// Execute submits the swap transaction for the given quote using the provided private key.
	Execute(ctx context.Context, quote Quote, privateKey *ecdsa.PrivateKey) (ExecuteResult, error)

	// CheckStatus checks the status of a swap by its source chain tx hash.
	// externalID is a provider-specific identifier (ignored by some providers).
	// Returns "pending", "completed", or "failed".
	CheckStatus(ctx context.Context, txHash string, externalID string) (string, error)
}
