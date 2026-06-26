package swaps

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// minGasTipCap is the floor priority fee (1 gwei) applied to outbound
// transactions. On idle chains like Avalanche the suggested tip can be ~0, which
// lets validators evict the tx from the mempool; a small floor keeps it
// attractive while costing only a fraction of a cent at our gas limits.
var minGasTipCap = big.NewInt(1e9)

// baseFeeHeadroom multiplies the current base fee when computing the gas fee
// cap, leaving room for the base fee to rise before the tx is included.
var baseFeeHeadroom = big.NewInt(2)

// SignAndBroadcast builds, signs, logs, and broadcasts an EIP-1559 dynamic-fee
// transaction. It is the single path for every on-chain send so gas pricing and
// raw-tx logging stay consistent across providers.
//
// The gas fee cap is set to baseFeeHeadroom*baseFee + tip, giving headroom for
// the base fee to rise before inclusion. Legacy fixed-gasPrice txs priced at
// ~baseFee were being silently dropped by Avalanche whenever the base fee ticked
// up; the headroom here prevents that.
//
// The signed raw transaction and its nonce are logged BEFORE broadcast so the
// exact bytes are recoverable even if the tx is later dropped from the mempool.
// label identifies the call site in the log line (e.g. "SimpleSwap USDC transfer").
func SignAndBroadcast(
	ctx context.Context,
	rpc *ethclient.Client,
	chainID *big.Int,
	key *ecdsa.PrivateKey,
	to common.Address,
	value *big.Int,
	gasLimit uint64,
	data []byte,
	label string,
) (*types.Transaction, error) {
	from := crypto.PubkeyToAddress(key.PublicKey)

	nonce, err := rpc.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("getting nonce: %w", err)
	}

	tip, err := rpc.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting gas tip: %w", err)
	}
	if tip.Cmp(minGasTipCap) < 0 {
		tip = new(big.Int).Set(minGasTipCap)
	}

	head, err := rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("getting chain head: %w", err)
	}
	if head.BaseFee == nil {
		return nil, fmt.Errorf("chain head missing base fee (chain not EIP-1559?)")
	}

	feeCap := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, baseFeeHeadroom), tip)

	signedTx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &to,
		Value:     value,
		Data:      data,
	}), types.LatestSignerForChainID(chainID), key)
	if err != nil {
		return nil, fmt.Errorf("signing tx: %w", err)
	}

	raw, err := signedTx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("marshaling signed tx: %w", err)
	}

	// Record the raw signed transaction and nonce BEFORE broadcasting so the
	// exact bytes survive even if the chain later drops the tx from the mempool.
	log.Printf("%s: broadcasting from=%s nonce=%d hash=%s baseFee=%s tip=%s feeCap=%s raw=%s",
		label, from.Hex(), nonce, signedTx.Hash().Hex(),
		head.BaseFee.String(), tip.String(), feeCap.String(), hexutil.Encode(raw))

	if err := rpc.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("sending tx: %w", err)
	}

	return signedTx, nil
}
