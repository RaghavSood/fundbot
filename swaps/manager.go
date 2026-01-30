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
func (m *Manager) BestQuote(ctx context.Context, toAsset Asset, usdAmount float64, destination string, sender common.Address) (*Quote, error) {
	var best *Quote

	for _, p := range m.providers {
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
