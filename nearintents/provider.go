package nearintents

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	oneclick "github.com/defuse-protocol/one-click-sdk-go"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/balances"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/thorchain"
)

var chainIDs = map[string]*big.Int{
	"avalanche": big.NewInt(43114),
	"base":      big.NewInt(8453),
}

const erc20TransferABI = `[{"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

type Provider struct {
	client     *Client
	rpcClients map[string]*ethclient.Client
}

func NewProvider(apiKey string, rpcClients map[string]*ethclient.Client, httpClient *http.Client) *Provider {
	return &Provider{
		client:     NewClient(apiKey, httpClient),
		rpcClients: rpcClients,
	}
}

func (p *Provider) Name() string {
	return "nearintents"
}

func (p *Provider) Category() string {
	return "dex"
}

func (p *Provider) SupportsAsset(asset swaps.Asset) bool {
	_, ok := AssetToTokenID(asset)
	return ok
}

func (p *Provider) Quote(ctx context.Context, toAsset swaps.Asset, usdAmount float64, destination string, sender common.Address) ([]swaps.Quote, error) {
	var destTokenID string
	var ok bool
	if toAsset.Hints != nil && toAsset.Hints.NearIntentsTokenID != "" {
		destTokenID = toAsset.Hints.NearIntentsTokenID
		ok = true
	} else {
		destTokenID, ok = AssetToTokenID(toAsset)
	}
	if !ok {
		return nil, fmt.Errorf("nearintents: unsupported target asset %s", toAsset)
	}

	requiredUSDC := new(big.Int).SetInt64(int64(usdAmount * 1e6))

	var quotes []swaps.Quote

	for _, chain := range SupportedSourceChains() {
		sourceTokenID, ok := SourceTokenID(chain)
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
			log.Printf("nearintents: error checking USDC balance on %s: %v", chain, err)
			continue
		}
		if bal.Cmp(requiredUSDC) < 0 {
			log.Printf("nearintents: skipping %s, insufficient USDC (have %s, need %s)", chain, bal, requiredUSDC)
			continue
		}

		// USDC has 6 decimals
		amount := fmt.Sprintf("%d", requiredUSDC.Int64())
		deadline := time.Now().Add(60 * time.Minute)

		quoteReq := *oneclick.NewQuoteRequest(
			false,          // dry
			"EXACT_INPUT",  // swapType
			100,            // slippageTolerance (1%)
			sourceTokenID,  // originAsset
			"ORIGIN_CHAIN", // depositType
			destTokenID,    // destinationAsset
			amount,         // amount
			sender.Hex(),   // refundTo
			"ORIGIN_CHAIN", // refundType
			destination,    // recipient
			"DESTINATION_CHAIN", // recipientType
			deadline,       // deadline
		)
		depositMode := "SIMPLE"
		quoteReq.DepositMode = &depositMode

		resp, err := p.client.GetQuote(ctx, quoteReq)
		if err != nil {
			log.Printf("nearintents quote for %s via %s failed: %v", toAsset, chain, err)
			continue
		}

		depositAddr := resp.Quote.GetDepositAddress()
		if depositAddr == "" {
			log.Printf("nearintents: no deposit address returned for %s via %s", toAsset, chain)
			continue
		}

		expectedOut := parseToBigInt(resp.Quote.AmountOut)

		quotes = append(quotes, swaps.Quote{
			Provider:          "nearintents",
			FromAsset:         mustParseAsset(chain),
			ToAsset:           toAsset,
			FromChain:         chain,
			InputAmountUSD:    usdAmount,
			InputAmount:       requiredUSDC,
			ExpectedOutput:    resp.Quote.AmountOutFormatted,
			ExpectedOutputRaw: expectedOut,
			ExtraData: map[string]interface{}{
				"nearintents_deposit_address": depositAddr,
				"nearintents_correlation_id":  resp.CorrelationId,
				"nearintents_destination":     destination,
			},
		})
	}

	if len(quotes) == 0 {
		return nil, fmt.Errorf("nearintents: no quotes available for %s", toAsset)
	}

	return quotes, nil
}

func (p *Provider) Execute(ctx context.Context, quote swaps.Quote, privateKey *ecdsa.PrivateKey) (swaps.ExecuteResult, error) {
	depositAddr, _ := quote.ExtraData["nearintents_deposit_address"].(string)
	if depositAddr == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("nearintents: missing deposit address in quote ExtraData")
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

	fromAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	txHash, err := transferERC20(ctx, rpc, chainID, privateKey, fromAddr, usdcAddr, common.HexToAddress(depositAddr), quote.InputAmount)
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("nearintents USDC transfer: %w", err)
	}

	// Submit tx hash to speed up processing (best-effort)
	if submitErr := p.client.SubmitDepositTx(ctx, txHash, depositAddr); submitErr != nil {
		log.Printf("nearintents: failed to submit deposit tx (non-fatal): %v", submitErr)
	}

	return swaps.ExecuteResult{
		TxHash:     txHash,
		ExternalID: depositAddr, // used for status polling
	}, nil
}

func (p *Provider) CheckStatus(ctx context.Context, txHash string, externalID string) (string, error) {
	if externalID == "" {
		return "pending", nil
	}

	status, err := p.client.GetExecutionStatus(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("nearintents get status: %w", err)
	}

	switch status {
	case "SUCCESS":
		return "completed", nil
	case "FAILED", "REFUNDED":
		return "failed", nil
	default:
		// PENDING_DEPOSIT, INCOMPLETE_DEPOSIT, PROCESSING, KNOWN_DEPOSIT_TX
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

	log.Printf("Near Intents USDC transfer sent: %s", signedTx.Hash().Hex())

	// Don't wait for mining - return immediately and let status polling handle confirmation
	return signedTx.Hash().Hex(), nil
}

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
// by removing the decimal point. Pads to 8 decimal places for comparison.
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
