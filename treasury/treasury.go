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
	store      *db.Store
}

// New creates a Treasury. client must be constructed with the same key.
func New(key *ecdsa.PrivateKey, client *intents.Client, rpcClients map[string]*ethclient.Client, store *db.Store) *Treasury {
	return &Treasury{
		key:        key,
		address:    crypto.PubkeyToAddress(key.PublicKey),
		client:     client,
		rpcClients: rpcClients,
		store:      store,
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

// BalanceReport is a best-effort snapshot of every treasury balance source.
// Each section carries its own error string so a failure in one (e.g. an
// unfunded NEAR account) never hides the others.
type BalanceReport struct {
	Account         string            `json:"account"`
	EVM             []ChainBalance    `json:"-"`
	EVMErr          string            `json:"evm_error,omitempty"`
	Public          map[string]string `json:"-"` // tokenID -> base units
	PublicErr       string            `json:"public_error,omitempty"`
	Confidential    map[string]string `json:"-"` // tokenID -> base units
	ConfidentialErr string            `json:"confidential_error,omitempty"`
}

// Report gathers all balance sources best-effort. It never returns an error;
// per-section failures are captured in the report's *Err fields.
func (t *Treasury) Report(ctx context.Context) BalanceReport {
	rep := BalanceReport{
		Account:      t.address.Hex(),
		Public:       map[string]string{},
		Confidential: map[string]string{},
	}

	if evm, err := t.EVMBalances(ctx); err != nil {
		rep.EVMErr = err.Error()
	} else {
		rep.EVM = evm
	}

	tokenIDs := usdcTokenIDs()
	if pub, err := t.client.PublicBalances(ctx, tokenIDs); err != nil {
		rep.PublicErr = err.Error()
	} else {
		for i, id := range tokenIDs {
			rep.Public[id] = pub[i]
		}
	}

	if conf, err := t.client.ConfidentialBalances(ctx, tokenIDs...); err != nil {
		rep.ConfidentialErr = err.Error()
	} else {
		for _, b := range conf {
			rep.Confidential[b.TokenID] = b.Available
		}
	}

	return rep
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

// DepositToConfidential moves USDC from the treasury EVM wallet directly into
// the service's confidential intents balance in one step, using NEAR's
// direct-to-confidential deposit (ORIGIN_CHAIN deposit -> CONFIDENTIAL_INTENTS
// recipient). This is the primary bootstrap/top-up primitive: it avoids the
// two-step deposit-to-public-then-shield dance. Returns the ledger entry ID
// and the on-chain tx hash.
func (t *Treasury) DepositToConfidential(ctx context.Context, chain string, amount *big.Int) (int64, string, error) {
	return t.depositFromEVM(ctx, chain, amount, intents.TypeConfidentialIntents, "confidential_deposit", "treasury EVM -> confidential balance")
}

// DepositToIntents moves USDC from the treasury EVM wallet into the service's
// public (Main) intents balance. Returns the ledger entry ID and the on-chain
// tx hash. Prefer DepositToConfidential for funding the confidential rail.
func (t *Treasury) DepositToIntents(ctx context.Context, chain string, amount *big.Int) (int64, string, error) {
	return t.depositFromEVM(ctx, chain, amount, intents.TypeIntents, "intents_deposit", "treasury EVM -> intents public balance")
}

// depositFromEVM quotes an ORIGIN_CHAIN deposit that lands in the given
// intents recipient type, sends the treasury USDC to the returned EVM deposit
// address, and records the ledger entry.
func (t *Treasury) depositFromEVM(ctx context.Context, chain string, amount *big.Int, recipientType, kind, note string) (int64, string, error) {
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
		RecipientType:    recipientType,
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

	tx, err := swaps.SignAndBroadcast(ctx, rpc, chainIDs[chain], t.key, thorchain.USDCContracts[chain], big.NewInt(0), 100000, data, "treasury "+kind)
	if err != nil {
		return 0, "", fmt.Errorf("sending deposit: %w", err)
	}
	txHash := tx.Hash().Hex()

	if err := t.client.SubmitDepositTx(ctx, txHash, q.DepositAddress); err != nil {
		log.Printf("treasury: submit deposit tx (non-fatal): %v", err)
	}

	id, err := t.store.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
		Direction:      "out",
		Kind:           kind,
		Chain:          chain,
		AmountUsdc:     amount.Int64(),
		TxHash:         txHash,
		DepositAddress: q.DepositAddress,
		TopupID:        sql.NullInt64{},
		Status:         "pending",
		Note:           note,
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

	id, err := t.store.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
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
	_, err := t.store.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
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
	_, err := t.store.InsertTreasuryLedger(ctx, db.InsertTreasuryLedgerParams{
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

// ReconcilePending polls 1-Click status for pending treasury ledger ops
// (intents deposits, shields, confidential swaps that carry a deposit address)
// and marks them completed/failed. Confidential swaps linked to a user topup
// are also tracked by the swap tracker; reconciling them here keeps the
// ledger's own status column accurate.
func (t *Treasury) ReconcilePending(ctx context.Context) {
	ops, err := t.store.ListPendingTreasuryOps(ctx)
	if err != nil {
		log.Printf("treasury: error listing pending ops: %v", err)
		return
	}
	for _, op := range ops {
		select {
		case <-ctx.Done():
			return
		default:
		}
		status, err := t.client.Status(ctx, op.DepositAddress)
		if err != nil {
			log.Printf("treasury: status check for ledger %d (%s): %v", op.ID, op.DepositAddress, err)
			continue
		}
		var newStatus string
		switch status {
		case "SUCCESS":
			newStatus = "completed"
		case "FAILED", "REFUNDED":
			newStatus = "failed"
		default:
			continue
		}
		if err := t.store.UpdateTreasuryLedgerStatus(ctx, db.UpdateTreasuryLedgerStatusParams{
			Status: newStatus,
			ID:     op.ID,
		}); err != nil {
			log.Printf("treasury: error updating ledger %d: %v", op.ID, err)
			continue
		}
		log.Printf("treasury: ledger %d (%s) -> %s", op.ID, op.Kind, newStatus)
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
