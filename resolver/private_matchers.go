package resolver

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/RaghavSood/fundbot/houdini"
	"github.com/RaghavSood/fundbot/simpleswap"
)

// simpleswapMatcher provides dynamic lookup of SimpleSwap currencies.
type simpleswapMatcher struct {
	client *simpleswap.Client

	mu sync.RWMutex
	// byContract maps lowercase "network:contractaddress" to currency symbol
	byContract map[string]string
	// bySymbol maps lowercase "network:symbol" to currency symbol
	bySymbol map[string]string
}

func newSimpleswapMatcher(client *simpleswap.Client) *simpleswapMatcher {
	return &simpleswapMatcher{
		client:     client,
		byContract: make(map[string]string),
		bySymbol:   make(map[string]string),
	}
}

// refresh fetches the currency list and rebuilds the indices.
func (m *simpleswapMatcher) refresh(ctx context.Context) error {
	if m.client == nil {
		return nil
	}

	currencies, err := m.client.GetAllCurrencies(ctx)
	if err != nil {
		return err
	}

	byContract := make(map[string]string)
	bySymbol := make(map[string]string)

	for _, c := range currencies {
		network := strings.ToLower(c.Network)
		symbol := strings.ToLower(c.Symbol)

		// Index by contract address if present
		if c.ContractAddress != "" {
			key := network + ":" + strings.ToLower(c.ContractAddress)
			byContract[key] = c.Symbol
		}

		// Index by network:symbol (e.g., "eth:usdc")
		key := network + ":" + symbol
		bySymbol[key] = c.Symbol
	}

	m.mu.Lock()
	m.byContract = byContract
	m.bySymbol = bySymbol
	m.mu.Unlock()

	log.Printf("resolver: loaded %d SimpleSwap currencies", len(currencies))
	return nil
}

// match tries to find a SimpleSwap symbol for the given chain and contract/symbol.
func (m *simpleswapMatcher) match(chain, symbol, contractAddr string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	network := normalizeChainToNetwork(chain)

	// Try contract address first
	if contractAddr != "" {
		key := network + ":" + strings.ToLower(contractAddr)
		if sym, ok := m.byContract[key]; ok {
			return sym, true
		}
	}

	// Try symbol
	key := network + ":" + strings.ToLower(symbol)
	if sym, ok := m.bySymbol[key]; ok {
		return sym, true
	}

	return "", false
}

// houdiniMatcher provides dynamic lookup of Houdini currencies.
type houdiniMatcher struct {
	client *houdini.Client

	mu sync.RWMutex
	// byContract maps lowercase "network:contractaddress" to currency ID
	byContract map[string]string
	// bySymbol maps lowercase "network:symbol" to currency ID
	bySymbol map[string]string
}

func newHoudiniMatcher(client *houdini.Client) *houdiniMatcher {
	return &houdiniMatcher{
		client:     client,
		byContract: make(map[string]string),
		bySymbol:   make(map[string]string),
	}
}

// refresh fetches the currency list and rebuilds the indices.
func (m *houdiniMatcher) refresh(ctx context.Context) error {
	if m.client == nil {
		return nil
	}

	currencies, err := m.client.GetCurrencies(ctx)
	if err != nil {
		return err
	}

	byContract := make(map[string]string)
	bySymbol := make(map[string]string)

	for _, c := range currencies {
		network := strings.ToLower(c.Network)
		symbol := strings.ToLower(c.Symbol)

		// Index by contract address if present
		if c.ContractAddress != "" {
			key := network + ":" + strings.ToLower(c.ContractAddress)
			byContract[key] = c.ID
		}

		// Index by network:symbol
		key := network + ":" + symbol
		bySymbol[key] = c.ID
	}

	m.mu.Lock()
	m.byContract = byContract
	m.bySymbol = bySymbol
	m.mu.Unlock()

	log.Printf("resolver: loaded %d Houdini currencies", len(currencies))
	return nil
}

// match tries to find a Houdini ID for the given chain and contract/symbol.
func (m *houdiniMatcher) match(chain, symbol, contractAddr string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	network := normalizeChainToNetwork(chain)

	// Try contract address first
	if contractAddr != "" {
		key := network + ":" + strings.ToLower(contractAddr)
		if id, ok := m.byContract[key]; ok {
			return id, true
		}
	}

	// Try symbol
	key := network + ":" + strings.ToLower(symbol)
	if id, ok := m.bySymbol[key]; ok {
		return id, true
	}

	return "", false
}

// normalizeChainToNetwork converts our chain notation to exchange network names.
func normalizeChainToNetwork(chain string) string {
	chain = strings.ToLower(chain)
	switch chain {
	case "eth", "ethereum":
		return "eth"
	case "avax", "avalanche":
		return "avaxc"
	case "base":
		return "base"
	case "bsc", "binance":
		return "bsc"
	case "arb", "arbitrum":
		return "arb"
	case "polygon", "matic":
		return "polygon"
	case "sol", "solana":
		return "sol"
	case "btc", "bitcoin":
		return "btc"
	default:
		return chain
	}
}
