package houdini

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/balances"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/thorchain"
)

// chainIDs for EVM chains
var chainIDs = map[string]*big.Int{
	"avalanche": big.NewInt(43114),
	"base":      big.NewInt(8453),
}

const erc20TransferABI = `[{"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

type Provider struct {
	client     *Client
	rpcClients map[string]*ethclient.Client
}

func NewProvider(apiKey, apiSecret string, rpcClients map[string]*ethclient.Client) *Provider {
	return &Provider{
		client:     NewClient(apiKey, apiSecret),
		rpcClients: rpcClients,
	}
}

func (p *Provider) Name() string {
	return "houdini"
}

func (p *Provider) Category() string {
	return "private"
}

func (p *Provider) SupportsAsset(asset swaps.Asset) bool {
	_, ok := AssetToSymbol(asset)
	return ok
}

func (p *Provider) Quote(ctx context.Context, toAsset swaps.Asset, usdAmount float64, destination string, sender common.Address) ([]swaps.Quote, error) {
	if usdAmount < 50 {
		return nil, fmt.Errorf("houdini: minimum swap amount is $50 (requested $%.2f)", usdAmount)
	}

	var toSymbol string
	var ok bool
	if toAsset.Hints != nil && toAsset.Hints.HoudiniSymbol != "" {
		toSymbol = toAsset.Hints.HoudiniSymbol
		ok = true
	} else {
		toSymbol, ok = AssetToSymbol(toAsset)
	}
	if !ok {
		return nil, fmt.Errorf("houdini: unsupported target asset %s", toAsset)
	}

	requiredUSDC := new(big.Int).SetInt64(int64(usdAmount * 1e6))

	var quotes []swaps.Quote

	for _, chain := range SupportedSourceChains() {
		fromSymbol, ok := SourceSymbol(chain)
		if !ok {
			continue
		}

		rpc, ok := p.rpcClients[chain]
		if !ok {
			continue
		}
		usdcAddr, ok := thorchain.USDCContracts[chain]
		if !ok {
			continue
		}
		bal, err := balances.USDCBalance(ctx, rpc, usdcAddr, sender)
		if err != nil {
			log.Printf("houdini: error checking USDC balance on %s: %v", chain, err)
			continue
		}
		if bal.Cmp(requiredUSDC) < 0 {
			log.Printf("houdini: skipping %s, insufficient USDC (have %s, need %s)", chain, bal, requiredUSDC)
			continue
		}

		quote, err := p.client.GetQuote(ctx, fromSymbol, toSymbol, usdAmount)
		if err != nil {
			log.Printf("houdini quote for %s via %s failed: %v", toAsset, chain, err)
			continue
		}

		expectedOut := parseToBigInt(fmt.Sprintf("%g", quote.AmountOut))

		inputAmount := new(big.Int).SetInt64(int64(usdAmount * 1e6))

		quotes = append(quotes, swaps.Quote{
			Provider:          "houdini",
			FromAsset:         mustParseAsset(chain),
			ToAsset:           toAsset,
			FromChain:         chain,
			InputAmountUSD:    usdAmount,
			InputAmount:       inputAmount,
			ExpectedOutput:    fmt.Sprintf("%g", quote.AmountOut),
			ExpectedOutputRaw: expectedOut,
			ExtraData: map[string]interface{}{
				"houdini_from":        fromSymbol,
				"houdini_to":          toSymbol,
				"houdini_destination": destination,
				"houdini_quote_id":    quote.QuoteID,
			},
		})
	}

	if len(quotes) == 0 {
		return nil, fmt.Errorf("houdini: no quotes available for %s", toAsset)
	}

	return quotes, nil
}

func (p *Provider) Execute(ctx context.Context, quote swaps.Quote, privateKey *ecdsa.PrivateKey) (swaps.ExecuteResult, error) {
	fromSymbol, _ := quote.ExtraData["houdini_from"].(string)
	toSymbol, _ := quote.ExtraData["houdini_to"].(string)
	if fromSymbol == "" || toSymbol == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini: missing exchange symbols in quote ExtraData")
	}

	destination, _ := quote.ExtraData["houdini_destination"].(string)
	if destination == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini: missing destination in quote ExtraData")
	}

	quoteID, _ := quote.ExtraData["houdini_quote_id"].(string)

	rpc, ok := p.rpcClients[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no RPC client for chain %s", quote.FromChain)
	}

	chainID, ok := chainIDs[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("unknown chain ID for %s", quote.FromChain)
	}

	usdcAddr, ok := thorchain.USDCContracts[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no USDC contract for %s", quote.FromChain)
	}

	exchange, err := p.client.CreateExchange(ctx, fromSymbol, toSymbol, quote.InputAmountUSD, destination, quoteID)
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini create exchange: %w", err)
	}

	log.Printf("Houdini exchange created: houdiniId=%s, deposit=%s", exchange.HoudiniID, exchange.SenderAddress)

	fromAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	txHash, err := transferERC20(ctx, rpc, chainID, privateKey, fromAddr, usdcAddr, common.HexToAddress(exchange.SenderAddress), quote.InputAmount)
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini USDC transfer: %w", err)
	}

	return swaps.ExecuteResult{
		TxHash:     txHash,
		ExternalID: exchange.HoudiniID,
	}, nil
}

func (p *Provider) CheckStatus(ctx context.Context, txHash string, externalID string) (string, error) {
	if externalID == "" {
		return "pending", nil
	}

	status, err := p.client.GetStatus(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("houdini get status: %w", err)
	}

	// Houdini uses numeric status codes:
	// 0 = waiting for deposit
	// 1 = deposit received / confirming
	// 2 = exchanging
	// 3 = sending
	// 4 = completed
	// 5 = failed/expired
	switch {
	case status.Status == 4:
		return "completed", nil
	case status.Status >= 5:
		return "failed", nil
	default:
		return "pending", nil
	}
}

func transferERC20(ctx context.Context, rpc *ethclient.Client, chainID *big.Int, key *ecdsa.PrivateKey, from, token, to common.Address, amount *big.Int) (string, error) {
	parsed, err := abi.JSON(strings.NewReader(erc20TransferABI))
	if err != nil {
		return "", err
	}

	data, err := parsed.Pack("transfer", to, amount)
	if err != nil {
		return "", err
	}

	nonce, err := rpc.PendingNonceAt(ctx, from)
	if err != nil {
		return "", fmt.Errorf("getting nonce: %w", err)
	}

	gasPrice, err := rpc.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("getting gas price: %w", err)
	}

	tx := types.NewTransaction(nonce, token, big.NewInt(0), 100000, gasPrice, data)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), key)
	if err != nil {
		return "", fmt.Errorf("signing transfer tx: %w", err)
	}

	if err := rpc.SendTransaction(ctx, signedTx); err != nil {
		return "", fmt.Errorf("sending transfer tx: %w", err)
	}

	log.Printf("Houdini USDC transfer sent: %s", signedTx.Hash().Hex())

	receipt, err := bind.WaitMined(ctx, rpc, signedTx)
	if err != nil {
		return "", fmt.Errorf("waiting for transfer: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return "", fmt.Errorf("transfer tx failed")
	}

	return signedTx.Hash().Hex(), nil
}

// XMRProvider is a Houdini provider variant that routes via anonymous XMR.
// It is excluded from normal routing and only activated by the "hxmr" hint.
type XMRProvider struct {
	client     *Client
	rpcClients map[string]*ethclient.Client
}

func NewXMRProvider(apiKey, apiSecret string, rpcClients map[string]*ethclient.Client) *XMRProvider {
	return &XMRProvider{
		client:     NewClient(apiKey, apiSecret),
		rpcClients: rpcClients,
	}
}

func (p *XMRProvider) Name() string     { return "houdini-xmr" }
func (p *XMRProvider) Category() string { return "xmr-private" }

func (p *XMRProvider) SupportsAsset(asset swaps.Asset) bool {
	_, ok := AssetToSymbol(asset)
	return ok
}

func (p *XMRProvider) Quote(ctx context.Context, toAsset swaps.Asset, usdAmount float64, destination string, sender common.Address) ([]swaps.Quote, error) {
	if usdAmount < 50 {
		return nil, fmt.Errorf("houdini-xmr: minimum swap amount is $50 (requested $%.2f)", usdAmount)
	}

	var toSymbol string
	var ok bool
	if toAsset.Hints != nil && toAsset.Hints.HoudiniSymbol != "" {
		toSymbol = toAsset.Hints.HoudiniSymbol
		ok = true
	} else {
		toSymbol, ok = AssetToSymbol(toAsset)
	}
	if !ok {
		return nil, fmt.Errorf("houdini-xmr: unsupported target asset %s", toAsset)
	}

	requiredUSDC := new(big.Int).SetInt64(int64(usdAmount * 1e6))

	var quotes []swaps.Quote

	for _, chain := range SupportedSourceChains() {
		fromSymbol, ok := SourceSymbol(chain)
		if !ok {
			continue
		}

		rpc, ok := p.rpcClients[chain]
		if !ok {
			continue
		}
		usdcAddr, ok := thorchain.USDCContracts[chain]
		if !ok {
			continue
		}
		bal, err := balances.USDCBalance(ctx, rpc, usdcAddr, sender)
		if err != nil {
			log.Printf("houdini-xmr: error checking USDC balance on %s: %v", chain, err)
			continue
		}
		if bal.Cmp(requiredUSDC) < 0 {
			continue
		}

		quote, err := p.client.GetQuoteXMR(ctx, fromSymbol, toSymbol, usdAmount)
		if err != nil {
			log.Printf("houdini-xmr quote for %s via %s failed: %v", toAsset, chain, err)
			continue
		}

		expectedOut := parseToBigInt(fmt.Sprintf("%g", quote.AmountOut))
		inputAmount := new(big.Int).SetInt64(int64(usdAmount * 1e6))

		quotes = append(quotes, swaps.Quote{
			Provider:          "houdini-xmr",
			FromAsset:         mustParseAsset(chain),
			ToAsset:           toAsset,
			FromChain:         chain,
			InputAmountUSD:    usdAmount,
			InputAmount:       inputAmount,
			ExpectedOutput:    fmt.Sprintf("%g", quote.AmountOut),
			ExpectedOutputRaw: expectedOut,
			ExtraData: map[string]interface{}{
				"houdini_from":          fromSymbol,
				"houdini_to":            toSymbol,
				"houdini_destination":   destination,
				"houdini_in_quote_id":   quote.InQuoteID,
				"houdini_out_quote_id":  quote.OutQuoteID,
			},
		})
	}

	if len(quotes) == 0 {
		return nil, fmt.Errorf("houdini-xmr: no quotes available for %s", toAsset)
	}

	return quotes, nil
}

func (p *XMRProvider) Execute(ctx context.Context, quote swaps.Quote, privateKey *ecdsa.PrivateKey) (swaps.ExecuteResult, error) {
	fromSymbol, _ := quote.ExtraData["houdini_from"].(string)
	toSymbol, _ := quote.ExtraData["houdini_to"].(string)
	if fromSymbol == "" || toSymbol == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini-xmr: missing exchange symbols in quote ExtraData")
	}

	destination, _ := quote.ExtraData["houdini_destination"].(string)
	if destination == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini-xmr: missing destination in quote ExtraData")
	}

	inQuoteID, _ := quote.ExtraData["houdini_in_quote_id"].(string)
	outQuoteID, _ := quote.ExtraData["houdini_out_quote_id"].(string)

	rpc, ok := p.rpcClients[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no RPC client for chain %s", quote.FromChain)
	}

	chainID, ok := chainIDs[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("unknown chain ID for %s", quote.FromChain)
	}

	usdcAddr, ok := thorchain.USDCContracts[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no USDC contract for %s", quote.FromChain)
	}

	exchange, err := p.client.CreateExchangeXMR(ctx, fromSymbol, toSymbol, quote.InputAmountUSD, destination, inQuoteID, outQuoteID)
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini-xmr create exchange: %w", err)
	}

	log.Printf("Houdini XMR exchange created: houdiniId=%s, deposit=%s", exchange.HoudiniID, exchange.SenderAddress)

	fromAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	txHash, err := transferERC20(ctx, rpc, chainID, privateKey, fromAddr, usdcAddr, common.HexToAddress(exchange.SenderAddress), quote.InputAmount)
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("houdini-xmr USDC transfer: %w", err)
	}

	return swaps.ExecuteResult{
		TxHash:     txHash,
		ExternalID: exchange.HoudiniID,
	}, nil
}

func (p *XMRProvider) CheckStatus(ctx context.Context, txHash string, externalID string) (string, error) {
	if externalID == "" {
		return "pending", nil
	}

	status, err := p.client.GetStatus(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("houdini-xmr get status: %w", err)
	}

	switch {
	case status.Status == 4:
		return "completed", nil
	case status.Status >= 5:
		return "failed", nil
	default:
		return "pending", nil
	}
}

// mustParseAsset returns a USDC asset for the given source chain.
func mustParseAsset(chain string) swaps.Asset {
	switch chain {
	case "avalanche":
		a, _ := swaps.ParseAsset("AVAX.USDC-0xB97EF9Ef8734C71904D8002F8B6BC66Dd9c48a6E")
		return a
	case "base":
		a, _ := swaps.ParseAsset("BASE.USDC-0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913")
		return a
	default:
		return swaps.Asset{Chain: strings.ToUpper(chain), Symbol: "USDC"}
	}
}

// parseToBigInt parses a decimal string like "0.00123456" to a big.Int
// by removing the decimal point. Multiplies by 1e8 for comparison.
func parseToBigInt(s string) *big.Int {
	parts := strings.SplitN(s, ".", 2)
	if len(parts) == 1 {
		val := new(big.Int)
		val.SetString(s, 10)
		val.Mul(val, big.NewInt(1e8))
		return val
	}

	frac := parts[1]
	if len(frac) > 8 {
		frac = frac[:8]
	}
	for len(frac) < 8 {
		frac += "0"
	}

	combined := parts[0] + frac
	val := new(big.Int)
	val.SetString(combined, 10)
	return val
}
