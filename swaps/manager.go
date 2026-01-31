package swaps

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"

	"github.com/ethereum/go-ethereum/common"
)

// Manager orchestrates swap providers and selects the best quote.
type Manager struct {
	providers []Provider
}

// NewManager creates a Manager with the given providers.
func NewManager(providers ...Provider) *Manager {
	return &Manager{providers: providers}
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
		return nil, fmt.Errorf("no quotes available for %s", toAsset)
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
