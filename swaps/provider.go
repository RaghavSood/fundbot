package swaps

import (
	"context"
	"crypto/ecdsa"
	"math/big"
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

// Provider is the interface that swap providers must implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "thorchain").
	Name() string

	// Quote returns quotes for swapping usdAmount worth of stablecoins to toAsset,
	// one per supported source chain. The destination is the recipient address on the target chain.
	Quote(ctx context.Context, toAsset Asset, usdAmount float64, destination string) ([]Quote, error)

	// Execute submits the swap transaction for the given quote using the provided private key.
	// Returns the transaction hash.
	Execute(ctx context.Context, quote Quote, privateKey *ecdsa.PrivateKey) (string, error)

	// CheckStatus checks the status of a swap by its source chain tx hash.
	// Returns "pending", "completed", or "failed".
	CheckStatus(ctx context.Context, txHash string) (string, error)
}
