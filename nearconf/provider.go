// Package nearconf implements the confidential swap provider: user USDC is
// paid to the service treasury on the origin chain, and the swap output is
// delivered from the treasury's Confidential Intents balance, leaving no
// public on-chain route between the user's deposit and the destination.
package nearconf

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/balances"
	"github.com/RaghavSood/fundbot/intents"
	"github.com/RaghavSood/fundbot/nearintents"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/thorchain"
	"github.com/RaghavSood/fundbot/treasury"
)

// ProviderName is the routing name for the confidential provider.
const ProviderName = "nearintents-confidential"

// Category marks providers that must never be selected without an explicit
// hint (see swaps.Manager.filterProviders).
const Category = "anon-private"

const erc20TransferABI = `[{"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

var chainIDs = map[string]*big.Int{
	"avalanche": big.NewInt(43114),
	"base":      big.NewInt(8453),
}

type Provider struct {
	treasury   *treasury.Treasury
	rpcClients map[string]*ethclient.Client
	bufferUSD  float64
}

func NewProvider(t *treasury.Treasury, rpcClients map[string]*ethclient.Client, bufferUSD float64) *Provider {
	if bufferUSD <= 0 {
		bufferUSD = 1
	}
	return &Provider{
		treasury:   t,
		rpcClients: rpcClients,
		bufferUSD:  bufferUSD,
	}
}

func (p *Provider) Name() string {
	return ProviderName
}

func (p *Provider) Category() string {
	return Category
}

func (p *Provider) SupportsAsset(asset swaps.Asset) bool {
	_, ok := nearintents.AssetToTokenID(asset)
	return ok
}

func (p *Provider) Quote(ctx context.Context, toAsset swaps.Asset, usdAmount float64, destination string, sender common.Address) ([]swaps.Quote, error) {
	var destTokenID string
	var ok bool
	if toAsset.Hints != nil && toAsset.Hints.NearIntentsTokenID != "" {
		destTokenID = toAsset.Hints.NearIntentsTokenID
		ok = true
	} else {
		destTokenID, ok = nearintents.AssetToTokenID(toAsset)
	}
	if !ok {
		return nil, fmt.Errorf("nearconf: unsupported target asset %s", toAsset)
	}

	requiredUSDC := new(big.Int).SetInt64(int64(usdAmount * 1e6))
	requiredWithBuffer := new(big.Int).SetInt64(int64((usdAmount + p.bufferUSD) * 1e6))

	var quotes []swaps.Quote

	for _, chain := range nearintents.SupportedSourceChains() {
		sourceTokenID, ok := nearintents.SourceTokenID(chain)
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

		// The user must be able to pay the treasury on this chain.
		bal, err := balances.USDCBalance(ctx, rpc, usdcAddr, sender)
		if err != nil {
			log.Printf("nearconf: error checking USDC balance on %s: %v", chain, err)
			continue
		}
		if bal.Cmp(requiredUSDC) < 0 {
			continue
		}

		// The treasury's confidential balance must cover the swap plus buffer.
		confBal, err := p.treasury.ConfidentialUSDC(ctx, chain)
		if err != nil {
			log.Printf("nearconf: error checking confidential balance for %s: %v", chain, err)
			continue
		}
		if confBal.Cmp(requiredWithBuffer) < 0 {
			log.Printf("nearconf: skipping %s, confidential balance too low (have %s, need %s)", chain, confBal, requiredWithBuffer)
			continue
		}

		q, err := p.treasury.Client().Quote(ctx, intents.QuoteParams{
			Dry:              true,
			OriginAsset:      sourceTokenID,
			DestinationAsset: destTokenID,
			Amount:           requiredUSDC.String(),
			DepositType:      intents.TypeConfidentialIntents,
			Recipient:        destination,
			RecipientType:    intents.TypeDestinationChain,
			RefundTo:         p.treasury.Client().SignerID(),
			RefundType:       intents.TypeConfidentialIntents,
		})
		if err != nil {
			log.Printf("nearconf quote for %s via %s failed: %v", toAsset, chain, err)
			continue
		}

		expectedOut, ok := new(big.Int).SetString(q.AmountOut, 10)
		if !ok {
			log.Printf("nearconf: bad amountOut %q", q.AmountOut)
			continue
		}

		quotes = append(quotes, swaps.Quote{
			Provider:          ProviderName,
			FromAsset:         usdcAsset(chain),
			ToAsset:           toAsset,
			FromChain:         chain,
			InputAmountUSD:    usdAmount,
			InputAmount:       requiredUSDC,
			ExpectedOutput:    q.AmountOutFmt,
			ExpectedOutputRaw: expectedOut,
			ExtraData: map[string]interface{}{
				"nearconf_origin_token": sourceTokenID,
				"nearconf_dest_token":   destTokenID,
				"nearconf_destination":  destination,
			},
		})
	}

	if len(quotes) == 0 {
		return nil, fmt.Errorf("nearconf: no quotes available for %s", toAsset)
	}
	return quotes, nil
}

// Execute runs the two-leg confidential topup:
// leg 1 - the user's wallet pays USDC to the treasury EVM address;
// leg 2 - the treasury funds the swap from its confidential balance.
// Leg 2 is fired immediately after leg 1 broadcasts; at gas-topup sizes the
// treasury fronts the output while the user payment confirms.
func (p *Provider) Execute(ctx context.Context, quote swaps.Quote, privateKey *ecdsa.PrivateKey) (swaps.ExecuteResult, error) {
	originToken, _ := quote.ExtraData["nearconf_origin_token"].(string)
	destToken, _ := quote.ExtraData["nearconf_dest_token"].(string)
	destination, _ := quote.ExtraData["nearconf_destination"].(string)
	if originToken == "" || destToken == "" || destination == "" {
		return swaps.ExecuteResult{}, fmt.Errorf("nearconf: missing swap parameters in quote ExtraData")
	}

	rpc, ok := p.rpcClients[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no RPC client for chain %s", quote.FromChain)
	}
	usdcAddr, ok := thorchain.USDCContracts[quote.FromChain]
	if !ok {
		return swaps.ExecuteResult{}, fmt.Errorf("no USDC contract for %s", quote.FromChain)
	}

	// Leg 1: user pays the treasury.
	parsed, err := abi.JSON(strings.NewReader(erc20TransferABI))
	if err != nil {
		return swaps.ExecuteResult{}, err
	}
	data, err := parsed.Pack("transfer", p.treasury.Address(), quote.InputAmount)
	if err != nil {
		return swaps.ExecuteResult{}, err
	}
	tx, err := swaps.SignAndBroadcast(ctx, rpc, chainIDs[quote.FromChain], privateKey, usdcAddr, big.NewInt(0), 100000, data, "confidential topup user payment")
	if err != nil {
		return swaps.ExecuteResult{}, fmt.Errorf("nearconf user payment: %w", err)
	}
	txHash := tx.Hash().Hex()
	log.Printf("nearconf: user payment sent from %s: %s", crypto.PubkeyToAddress(privateKey.PublicKey).Hex(), txHash)
	p.treasury.RecordUserPayment(ctx, quote.FromChain, quote.InputAmount, txHash, 0)

	// Leg 2: fund the swap from the confidential balance.
	q, err := p.treasury.Client().Quote(ctx, intents.QuoteParams{
		Dry:              false,
		OriginAsset:      originToken,
		DestinationAsset: destToken,
		Amount:           quote.InputAmount.String(),
		DepositType:      intents.TypeConfidentialIntents,
