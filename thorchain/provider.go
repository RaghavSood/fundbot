package thorchain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/balances"
	"github.com/RaghavSood/fundbot/swaps"
)

// ChainIDs for EVM chains
var chainIDs = map[string]*big.Int{
	"avalanche": big.NewInt(43114),
	"base":      big.NewInt(8453),
}

type Provider struct {
	client     *Client
	rpcClients map[string]*ethclient.Client // keyed by "avalanche", "base"
}

func NewProvider(rpcClients map[string]*ethclient.Client) *Provider {
	return &Provider{
		client:     NewClient(),
		rpcClients: rpcClients,
	}
}

func (p *Provider) Name() string {
	return "thorchain"
}

func (p *Provider) Category() string {
	return "dex"
}

func (p *Provider) Quote(ctx context.Context, toAsset swaps.Asset, usdAmount float64, destination string, sender common.Address) ([]swaps.Quote, error) {
	// USDC has 6 decimals; Thorchain expects 1e8, so multiply USD by 1e8
	// (1 USDC = 1 USD, 6 decimals native, thorchain uses 8 decimal representation)
	thorAmount := int64(usdAmount * 1e8)

	// Required USDC in smallest unit (6 decimals)
	requiredUSDC := new(big.Int).SetInt64(int64(usdAmount * 1e6))

	var quotes []swaps.Quote

	for rpcKey, tcAsset := range SourceAssets {
		// Check USDC balance on this chain
		rpc, ok := p.rpcClients[rpcKey]
		if !ok {
			continue
		}
		usdcAddr, ok := USDCContracts[rpcKey]
		if !ok {
			continue
		}
		bal, err := balances.USDCBalance(ctx, rpc, usdcAddr, sender)
		if err != nil {
			log.Printf("thorchain: error checking USDC balance on %s: %v", rpcKey, err)
			continue
		}
		if bal.Cmp(requiredUSDC) < 0 {
			log.Printf("thorchain: skipping %s, insufficient USDC (have %s, need %s)", rpcKey, bal, requiredUSDC)
			continue
		}

		quoteResp, err := p.client.GetQuote(ctx, tcAsset, toAsset.String(), destination, thorAmount)
		if err != nil {
			log.Printf("thorchain quote for %s via %s failed: %v", toAsset, rpcKey, err)
			continue
		}

		// Convert input USD to USDC smallest unit (6 decimals)
		inputAmount := new(big.Int)
		inputAmount.SetInt64(int64(usdAmount * 1e6))

		expectedOut := new(big.Int)
		expectedOut.SetString(quoteResp.ExpectedAmountOut, 10)

		quotes = append(quotes, swaps.Quote{
			Provider:          "thorchain",
			FromAsset:         mustParseAsset(tcAsset),
			ToAsset:           toAsset,
			FromChain:         rpcKey,
			InputAmountUSD:    usdAmount,
			InputAmount:       inputAmount,
			ExpectedOutput:    quoteResp.ExpectedAmountOut,
			ExpectedOutputRaw: expectedOut,
			Memo:              quoteResp.Memo,
			Router:            quoteResp.Router,
			VaultAddress:      quoteResp.InboundAddress,
			Expiry:            quoteResp.Expiry,
			ExtraData: map[string]interface{}{
				"fees":              quoteResp.Fees,
				"recommended_min":   quoteResp.RecommendedMinIn,
				"gas_rate":          quoteResp.RecommendedGasRate,
				"outbound_delay_s":  quoteResp.OutboundDelaySecs,
			},
		})
	}

	if len(quotes) == 0 {
		return nil, fmt.Errorf("no thorchain quotes available for %s", toAsset)
	}

	return quotes, nil
}

func (p *Provider) Execute(ctx context.Context, quote swaps.Quote, privateKey *ecdsa.PrivateKey) (swaps.ExecuteResult, error) {
	rpc, ok := p.rpcClients[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no RPC client for chain %s", quote.FromChain)
	}

	chainID, ok := chainIDs[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("unknown chain ID for %s", quote.FromChain)
	}

	usdcAddr, ok := USDCContracts[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no USDC contract for %s", quote.FromChain)
	}

	routerAddr := common.HexToAddress(quote.Router)
	vaultAddr := common.HexToAddress(quote.VaultAddress)
	fromAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	// Step 1: Approve router to spend USDC
	if err := p.approveERC20(ctx, rpc, chainID, privateKey, fromAddr, usdcAddr, routerAddr, quote.InputAmount); err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("approving USDC: %w", err)
	}

	// Step 2: Call depositWithExpiry on router
	txHash, err := p.depositWithExpiry(ctx, rpc, chainID, privateKey, fromAddr, routerAddr, vaultAddr, usdcAddr, quote.InputAmount, quote.Memo, quote.Expiry)
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("deposit: %w", err)
	}

	return swaps.ExecuteResult{TxHash: txHash}, nil
}

func (p *Provider) approveERC20(ctx context.Context, rpc *ethclient.Client, chainID *big.Int, key *ecdsa.PrivateKey, from, token, spender common.Address, amount *big.Int) error {
	parsed, err := abi.JSON(strings.NewReader(ERC20ApproveABI))
	if err != nil {
		return err
	}

	data, err := parsed.Pack("approve", spender, amount)
	if err != nil {
		return err
	}

	nonce, err := rpc.PendingNonceAt(ctx, from)
	if err != nil {
		return fmt.Errorf("getting nonce: %w", err)
	}

	gasPrice, err := rpc.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("getting gas price: %w", err)
	}

	tx := types.NewTransaction(nonce, token, big.NewInt(0), 100000, gasPrice, data)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), key)
	if err != nil {
		return fmt.Errorf("signing approve tx: %w", err)
	}

	if err := rpc.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("sending approve tx: %w", err)
	}

	log.Printf("Approve tx sent: %s", signedTx.Hash().Hex())

	// Wait for approval to be mined
	receipt, err := bind.WaitMined(ctx, rpc, signedTx)
	if err != nil {
		return fmt.Errorf("waiting for approve: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("approve tx failed")
	}

	return nil
}

func (p *Provider) depositWithExpiry(ctx context.Context, rpc *ethclient.Client, chainID *big.Int, key *ecdsa.PrivateKey, from, router, vault, asset common.Address, amount *big.Int, memo string, expiry int64) (string, error) {
	parsed, err := abi.JSON(strings.NewReader(RouterDepositABI))
	if err != nil {
		return "", err
	}

	// Ensure expiry is at least 60 min in the future
	minExpiry := time.Now().Unix() + 3600
	if expiry < minExpiry {
		expiry = minExpiry
	}

	data, err := parsed.Pack("depositWithExpiry", vault, asset, amount, memo, big.NewInt(expiry))
	if err != nil {
		return "", fmt.Errorf("packing deposit: %w", err)
	}

	nonce, err := rpc.PendingNonceAt(ctx, from)
	if err != nil {
		return "", fmt.Errorf("getting nonce: %w", err)
	}

	gasPrice, err := rpc.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("getting gas price: %w", err)
	}

	// ERC20 deposit: value is 0 (tokens transferred via approve+transferFrom)
	tx := types.NewTransaction(nonce, router, big.NewInt(0), 200000, gasPrice, data)
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), key)
	if err != nil {
		return "", fmt.Errorf("signing deposit tx: %w", err)
	}

	if err := rpc.SendTransaction(ctx, signedTx); err != nil {
		return "", fmt.Errorf("sending deposit tx: %w", err)
	}

	log.Printf("Deposit tx sent: %s", signedTx.Hash().Hex())

	return signedTx.Hash().Hex(), nil
}

func (p *Provider) CheckStatus(ctx context.Context, txHash string, externalID string) (string, error) {
	status, err := p.client.GetTxStatus(ctx, txHash)
	if err != nil {
		return "", err
	}

	// Cross-chain swaps: completed when outbound is signed
	if status.Stages.OutboundSigned != nil && status.Stages.OutboundSigned.Completed {
		return "completed", nil
	}

	// Native Thorchain swaps (e.g. to RUNE): no outbound_signed stage,
	// completed when swap is finalised
	if status.Stages.OutboundSigned == nil &&
		status.Stages.SwapFinalised != nil && status.Stages.SwapFinalised.Completed {
		return "completed", nil
	}

	return "pending", nil
}

func mustParseAsset(s string) swaps.Asset {
	a, err := swaps.ParseAsset(s)
	if err != nil {
		panic(err)
	}
	return a
}
