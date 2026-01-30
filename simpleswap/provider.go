package simpleswap

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

func NewProvider(apiKey string, rpcClients map[string]*ethclient.Client) *Provider {
	return &Provider{
		client:     NewClient(apiKey),
		rpcClients: rpcClients,
	}
}

func (p *Provider) Name() string {
	return "simpleswap"
}

func (p *Provider) Quote(ctx context.Context, toAsset swaps.Asset, usdAmount float64, destination string) ([]swaps.Quote, error) {
	toSymbol, ok := AssetToSymbol(toAsset)
	if !ok {
		return nil, fmt.Errorf("simpleswap: unsupported target asset %s", toAsset)
	}

	var quotes []swaps.Quote

	for _, chain := range SupportedSourceChains() {
		fromSymbol, ok := SourceSymbol(chain)
		if !ok {
			continue
		}

		// SimpleSwap amount is in USDC units (e.g. 5.00 for $5)
		estimated, err := p.client.GetEstimated(ctx, fromSymbol, toSymbol, usdAmount)
		if err != nil {
			log.Printf("simpleswap quote for %s via %s failed: %v", toAsset, chain, err)
			continue
		}

		// Parse estimated output as a big.Int (raw units depend on the asset)
		expectedOut := parseToBigInt(estimated)

		// Input in USDC smallest unit (6 decimals)
		inputAmount := new(big.Int)
		inputAmount.SetInt64(int64(usdAmount * 1e6))

		quotes = append(quotes, swaps.Quote{
			Provider:          "simpleswap",
			FromAsset:         mustParseAsset(chain),
			ToAsset:           toAsset,
			FromChain:         chain,
			InputAmountUSD:    usdAmount,
			InputAmount:       inputAmount,
			ExpectedOutput:    estimated,
			ExpectedOutputRaw: expectedOut,
			ExtraData: map[string]interface{}{
				"simpleswap_from":        fromSymbol,
				"simpleswap_to":          toSymbol,
				"simpleswap_destination": destination,
			},
		})
	}

	if len(quotes) == 0 {
		return nil, fmt.Errorf("simpleswap: no quotes available for %s", toAsset)
	}

	return quotes, nil
}

func (p *Provider) Execute(ctx context.Context, quote swaps.Quote, privateKey *ecdsa.PrivateKey) (swaps.ExecuteResult, error) {
	fromSymbol, _ := quote.ExtraData["simpleswap_from"].(string)
	toSymbol, _ := quote.ExtraData["simpleswap_to"].(string)
	if fromSymbol == "" || toSymbol == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("simpleswap: missing exchange symbols in quote ExtraData")
	}

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

	destination, _ := quote.ExtraData["simpleswap_destination"].(string)
	if destination == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("simpleswap: missing destination in quote ExtraData")
	}

	fromAddr := crypto.PubkeyToAddress(privateKey.PublicKey)
	amountStr := fmt.Sprintf("%g", quote.InputAmountUSD)

	// Create exchange on SimpleSwap
	exchange, err := p.client.CreateExchange(ctx, fromSymbol, toSymbol, amountStr, destination, fromAddr.Hex())
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("simpleswap create exchange: %w", err)
	}

	log.Printf("SimpleSwap exchange created: id=%s, deposit=%s", exchange.ID, exchange.AddressFrom)

	// Send USDC to the deposit address via ERC20 transfer
	txHash, err := p.transferERC20(ctx, rpc, chainID, privateKey, fromAddr, usdcAddr, common.HexToAddress(exchange.AddressFrom), quote.InputAmount)
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("simpleswap USDC transfer: %w", err)
	}

	return swaps.ExecuteResult{
		TxHash:     txHash,
		ExternalID: exchange.ID,
	}, nil
}

func (p *Provider) CheckStatus(ctx context.Context, txHash string, externalID string) (string, error) {
	if externalID == "" {
		return "pending", nil
	}

	exchange, err := p.client.GetExchange(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("simpleswap get exchange: %w", err)
	}

	switch exchange.Status {
	case "finished":
		return "completed", nil
	case "failed", "refunded", "expired":
		return "failed", nil
	default:
		// waiting, confirming, exchanging, sending
		return "pending", nil
	}
}

func (p *Provider) transferERC20(ctx context.Context, rpc *ethclient.Client, chainID *big.Int, key *ecdsa.PrivateKey, from, token, to common.Address, amount *big.Int) (string, error) {
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

	log.Printf("SimpleSwap USDC transfer sent: %s", signedTx.Hash().Hex())

	// Wait for mining
	receipt, err := bind.WaitMined(ctx, rpc, signedTx)
	if err != nil {
		return "", fmt.Errorf("waiting for transfer: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return "", fmt.Errorf("transfer tx failed")
	}

	return signedTx.Hash().Hex(), nil
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
// by removing the decimal point (treating as raw integer representation).
// For comparison purposes, we multiply by 1e8 to get a common base.
func parseToBigInt(s string) *big.Int {
	// Remove decimal point and parse as integer
	parts := strings.SplitN(s, ".", 2)
	if len(parts) == 1 {
		val := new(big.Int)
		val.SetString(s, 10)
		// Multiply by 1e8 for comparison
		val.Mul(val, big.NewInt(1e8))
		return val
	}

	// Pad fractional part to 8 decimal places
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
