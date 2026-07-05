// Package treasury manages the service-level Near Intents account: an EVM
// key (m/44'/60'/1'/0/0) that owns USDC float on Base/Avalanche, a public
// balance on intents.near, and a confidential balance used to fund private
// swaps.
package treasury

import (
	"context"
	"crypto/ecdsa"
	"database/sql"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/balances"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/intents"
	"github.com/RaghavSood/fundbot/nearintents"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/thorchain"
)

const erc20TransferABI = `[{"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

var chainIDs = map[string]*big.Int{
	"avalanche": big.NewInt(43114),
	"base":      big.NewInt(8453),
}

// Treasury coordinates the treasury EVM wallet and the service intents account.
type Treasury struct {
	key        *ecdsa.PrivateKey
	address    common.Address
	client     *intents.Client
	rpcClients map[string]*ethclient.Client
	queries    *db.Queries
}

// New creates a Treasury. client must be constructed with the same key.
func New(key *ecdsa.PrivateKey, client *intents.Client, rpcClients map[string]*ethclient.Client, queries *db.Queries) *Treasury {
	return &Treasury{
		key:        key,
		address:    crypto.PubkeyToAddress(key.PublicKey),
		client:     client,
		rpcClients: rpcClients,
		queries:    queries,
	}
}

// Address returns the treasury EVM address (also the intents signer account).
func (t *Treasury) Address() common.Address {
	return t.address
}

// Client returns the underlying intents client.
func (t *Treasury) Client() *intents.Client {
	return t.client
}

// ChainBalance holds a treasury balance for one chain, in USDC base units.
type ChainBalance struct {
	Chain   string
	Balance *big.Int
}

// EVMBalances returns treasury USDC balances on all configured chains.
func (t *Treasury) EVMBalances(ctx context.Context) ([]ChainBalance, error) {
	var out []ChainBalance
	for _, chain := range nearintents.SupportedSourceChains() {
		rpc, ok := t.rpcClients[chain]
		if !ok {
			continue
		}
		usdcAddr, ok := thorchain.USDCContracts[chain]
		if !ok {
			continue
		}
		bal, err := balances.USDCBalance(ctx, rpc, usdcAddr, t.address)
		if err != nil {
			return nil, fmt.Errorf("treasury balance on %s: %w", chain, err)
		}
		out = append(out, ChainBalance{Chain: chain, Balance: bal})
	}
	return out, nil
}

// IntentsBalances returns public (Main) and confidential balances of the
// per-chain USDC representations held by the service intents account, keyed
// by 1-Click token ID. Amounts are base-unit strings.
func (t *Treasury) IntentsBalances(ctx context.Context) (public map[string]string, confidential map[string]string, err error) {
	tokenIDs := usdcTokenIDs()

	pub, err := t.client.PublicBalances(ctx, tokenIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("public intents balances: %w", err)
	}
	public = make(map[string]string, len(tokenIDs))
	for i, id := range tokenIDs {
		public[id] = pub[i]
	}

	conf, err := t.client.ConfidentialBalances(ctx, tokenIDs...)
	if err != nil {
		return nil, nil, fmt.Errorf("confidential intents balances: %w", err)
	}
	confidential = make(map[string]string, len(conf))
	for _, b := range conf {
		confidential[b.TokenID] = b.Available
	}

	return public, confidential, nil
}

// ConfidentialUSDC returns the confidential balance (base units) of the USDC
// representation for the given source chain. Missing balances return 0.
func (t *Treasury) ConfidentialUSDC(ctx context.Context, chain string) (*big.Int, error) {
	tokenID, ok := nearintents.SourceTokenID(chain)
	if !ok {
		return nil, fmt.Errorf("unknown source chain %q", chain)
	}
	bals, err := t.client.ConfidentialBalances(ctx, tokenID)
	if err != nil {
		return nil, err
	}
	for _, b := range bals {
		if b.TokenID == tokenID {
			val, ok := new(big.Int).SetString(b.Available, 10)
			if !ok {
				return nil, fmt.Errorf("bad balance %q for %s", b.Available, tokenID)
			}
			return val, nil
		}
	}
	return big.NewInt(0), nil
}

// DepositToIntents moves USDC from the treasury EVM wallet into the service's
// public intents balance. Returns the ledger entry ID and the on-chain tx hash.
func (t *Treasury) DepositToIntents(ctx context.Context, chain string, amount *big.Int) (int64, string, error) {
	tokenID, ok := nearintents.SourceTokenID(chain)
	if !ok {
		return 0, "", fmt.Errorf("unknown source chain %q", chain)
	}
	rpc, ok := t.rpcClients[chain]
	if !ok {
		return 0, "", fmt.Errorf("no RPC client for %s", chain)
	}

	q, err := t.client.Quote(ctx, intents.QuoteParams{
		Dry:              false,
		OriginAsset:      tokenID,
		DestinationAsset: tokenID,
		Amount:           amount.String(),
		DepositType:      intents.TypeOriginChain,
		Recipient:        t.client.SignerID(),
		RecipientType:    intents.TypeIntents,
		RefundTo:         strings.ToLower(t.address.Hex()),
		RefundType:       intents.TypeOriginChain,
	})
	if err != nil {
		return 0, "", fmt.Errorf("deposit quote: %w", err)
	}
	if q.DepositAddress == "" {
		return 0, "", fmt.Errorf("deposit quote returned no deposit address")
	}

	parsed, err := abi.JSON(strings.NewReader(erc20TransferABI))
	if err != nil {
		return 0, "", err
	}
	data, err := parsed.Pack("transfer", common.HexToAddress(q.DepositAddress), amount)
	if err != nil {
		return 0, "", err
	}

	tx, err := swaps.SignAndBroadcast(ctx, rpc, chainIDs[chain], t.key, thorchain.USDCContracts[chain], big.NewInt(0), 100000, data, "treasury intents deposit")
	if err != nil {
		return 0, "", fmt.Errorf("sending deposit: %w", err)
	}
	txHash := tx.Hash().Hex()

	if err := t.client.SubmitDepositTx(ctx, txHash, q.DepositAddress); err != nil {
		log.Printf("treasury: submit deposit tx (non-fatal): %v", err)
	}

	id, err := t.queries.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
		Direction:      "out",
		Kind:           "intents_deposit",
		Chain:          chain,
		AmountUsdc:     amount.Int64(),
		TxHash:         txHash,
		DepositAddress: q.DepositAddress,
		TopupID:        sql.NullInt64{},
		Status:         "pending",
		Note:           "treasury EVM -> intents public balance",
	})
	if err != nil {
		log.Printf("treasury: ledger insert failed (deposit sent %s): %v", txHash, err)
	}
	return id, txHash, nil
}

// Shield moves USDC from the public intents balance into the confidential
// balance. Returns the ledger entry ID and the 1-Click deposit address used
// for status tracking.
func (t *Treasury) Shield(ctx context.Context, chain string, amount *big.Int) (int64, string, error) {
	tokenID, ok := nearintents.SourceTokenID(chain)
	if !ok {
		return 0, "", fmt.Errorf("unknown source chain %q", chain)
	}

	q, err := t.client.Quote(ctx, intents.QuoteParams{
		Dry:              false,
		OriginAsset:      tokenID,
		DestinationAsset: tokenID,
		Amount:           amount.String(),
		DepositType:      intents.TypeIntents,
		Recipient:        t.client.SignerID(),
		RecipientType:    intents.TypeConfidentialIntents,
		RefundTo:         t.client.SignerID(),
		RefundType:       intents.TypeIntents,
	})
	if err != nil {
		return 0, "", fmt.Errorf("shield quote: %w", err)
	}
	if q.DepositAddress == "" {
		return 0, "", fmt.Errorf("shield quote returned no deposit address")
	}

	if _, err := t.client.ExecuteFromBalance(ctx, q.DepositAddress); err != nil {
		return 0, "", fmt.Errorf("executing shield intent: %w", err)
	}

	id, err := t.queries.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
		Direction:      "out",
		Kind:           "shield",
		Chain:          chain,
		AmountUsdc:     amount.Int64(),
		DepositAddress: q.DepositAddress,
		TopupID:        sql.NullInt64{},
		Status:         "pending",
		Note:           "intents public -> confidential balance",
	})
	if err != nil {
		log.Printf("treasury: ledger insert failed (shield %s): %v", q.DepositAddress, err)
	}
	return id, q.DepositAddress, nil
}

// RecordUserPayment records an inbound user USDC payment to the treasury.
func (t *Treasury) RecordUserPayment(ctx context.Context, chain string, amount *big.Int, txHash string, topupID int64) {
	_, err := t.queries.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
		Direction:  "in",
		Kind:       "user_payment",
		Chain:      chain,
		AmountUsdc: amount.Int64(),
		TxHash:     txHash,
		TopupID:    sql.NullInt64{Int64: topupID, Valid: topupID != 0},
		Status:     "completed",
		Note:       "user payment for confidential topup",
	})
	if err != nil {
		log.Printf("treasury: ledger insert failed (user payment %s): %v", txHash, err)
	}
}

// RecordConfidentialSwap records an outbound confidential swap funded from
// the confidential balance.
func (t *Treasury) RecordConfidentialSwap(ctx context.Context, chain string, amount *big.Int, depositAddress string, topupID int64) {
	_, err := t.queries.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
		Direction:      "out",
		Kind:           "confidential_swap",
		Chain:          chain,
		AmountUsdc:     amount.Int64(),
		DepositAddress: depositAddress,
		TopupID:        sql.NullInt64{Int64: topupID, Valid: topupID != 0},
		Status:         "pending",
		Note:           "confidential swap to user destination",
	})
	if err != nil {
		log.Printf("treasury: ledger insert failed (confidential swap %s): %v", depositAddress, err)
	}
}

func usdcTokenIDs() []string {
	chains := nearintents.SupportedSourceChains()
	ids := make([]string, 0, len(chains))
	for _, c := range chains {
		if id, ok := nearintents.SourceTokenID(c); ok {
			ids = append(ids, id)
		}
	}
	return ids
}
