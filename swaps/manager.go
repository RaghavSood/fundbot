package swaps

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/balances"
)

// Manager orchestrates swap providers and selects the best quote.
type Manager struct {
	providers     []Provider
	rpcClients    map[string]*ethclient.Client
	usdcContracts map[string]common.Address
}

// NewManager creates a Manager with the given providers.
func NewManager(rpcClients map[string]*ethclient.Client, usdcContracts map[string]common.Address, providers ...Provider) *Manager {
	return &Manager{
		providers:     providers,
		rpcClients:    rpcClients,
		usdcContracts: usdcContracts,
	}
}

// BestQuote queries all providers and returns the quote with the highest expected output.
// sender is the EVM address that will fund the swap.
func (m *Manager) BestQuote(ctx context.Context, toAsset Asset, usdAmount float64, destination string, sender common.Address, hint RoutingHint) (*Quote, error) {
	providers, err := m.filterProviders(hint)
	if err != nil {
		return nil, err
	}

	var best *Quote

	for _, p := range providers {
		quotes, err := p.Quote(ctx, toAsset, usdAmount, destination, sender)
		if err != nil {
			log.Printf("provider %s quote error: %v", p.Name(), err)
			continue
		}

		for i := range quotes {
			q := &quotes[i]
			if best == nil || q.ExpectedOutputRaw.Cmp(best.ExpectedOutputRaw) > 0 {
				best = q
			}
		}
	}

	if best == nil {
		return nil, m.noQuotesError(ctx, toAsset, usdAmount, sender)
	}

	return best, nil
}

// filterProviders returns the subset of providers matching the routing hint.
func (m *Manager) filterProviders(hint RoutingHint) ([]Provider, error) {
	if hint.Type == "" {
		return m.providers, nil
	}

	var filtered []Provider
	for _, p := range m.providers {
		switch hint.Type {
		case "provider":
			if p.Name() == hint.Value {
				filtered = append(filtered, p)
			}
		case "category":
			if p.Category() == hint.Value {
				filtered = append(filtered, p)
			}
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no providers match routing hint %q", hint.Value)
	}

	return filtered, nil
}

// ExecuteSwap executes the given quote.
func (m *Manager) ExecuteSwap(ctx context.Context, quote *Quote, privateKey *ecdsa.PrivateKey) (ExecuteResult, error) {
	for _, p := range m.providers {
		if p.Name() == quote.Provider {
			return p.Execute(ctx, *quote, privateKey)
		}
	}
	return ExecuteResult{}, fmt.Errorf("provider %q not found", quote.Provider)
}

// CheckStatus checks the status of a swap via the named provider.
func (m *Manager) CheckStatus(ctx context.Context, provider, txHash, externalID string) (string, error) {
	for _, p := range m.providers {
		if p.Name() == provider {
			return p.CheckStatus(ctx, txHash, externalID)
		}
	}
	return "", fmt.Errorf("provider %q not found", provider)
}

// IsStaticallyKnown returns true if any provider has a static mapping for the asset.
func (m *Manager) IsStaticallyKnown(asset Asset) bool {
	for _, p := range m.providers {
		if p.SupportsAsset(asset) {
			return true
		}
	}
	return false
}

// noQuotesError builds a descriptive error when no quotes are available,
// checking whether insufficient balance is the cause.
func (m *Manager) noQuotesError(ctx context.Context, toAsset Asset, usdAmount float64, sender common.Address) error {
	requiredUSDC := new(big.Int).SetInt64(int64(usdAmount * 1e6))

	var lines []string
	allInsufficient := true
	checkedAny := false

	for chain, rpc := range m.rpcClients {
		usdcAddr, ok := m.usdcContracts[chain]
		if !ok {
			continue
		}
		bal, err := balances.USDCBalance(ctx, rpc, usdcAddr, sender)
		if err != nil {
			log.Printf("noQuotesError: error checking %s balance: %v", chain, err)
			continue
		}
		checkedAny = true

		// Format as human-readable USDC (6 decimals)
		whole := new(big.Int).Div(bal, big.NewInt(1e6))
		frac := new(big.Int).Mod(bal, big.NewInt(1e6))
		balStr := fmt.Sprintf("%d.%06d", whole.Int64(), frac.Int64())
		lines = append(lines, fmt.Sprintf("  %s: %s USDC", strings.Title(chain), balStr))

		if bal.Cmp(requiredUSDC) >= 0 {
			allInsufficient = false
		}
	}

	if checkedAny && allInsufficient {
		return fmt.Errorf("insufficient USDC balance for $%.2f swap to %s\nCurrent balances:\n%s",
			usdAmount, toAsset, strings.Join(lines, "\n"))
	}

	return fmt.Errorf("no quotes available for %s", toAsset)
}
