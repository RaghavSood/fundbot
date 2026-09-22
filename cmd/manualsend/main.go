// Command manualsend sends an ERC20 transfer from a derived wallet, mirroring
// the exact path SimpleSwap deposits take (swaps.SignAndBroadcast, gas 100000).
// One-off operational tool for funding an exchange whose automated transfer
// failed; refuses to send unless the derived address matches -expect.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/wallet"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const erc20ABI = `[{"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"name":"owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

func main() {
	configPath := flag.String("config", "", "path to fundbot config.json")
	index := flag.Uint("index", 0, "wallet derivation index")
	expect := flag.String("expect", "", "expected sender address (safety check)")
	token := flag.String("token", "", "ERC20 token contract address")
	to := flag.String("to", "", "recipient address")
	amount := flag.Int64("amount", 0, "token amount in base units")
	chain := flag.String("chain", "avalanche", "rpc_endpoints key to use")
	rpcOverride := flag.String("rpc", "", "RPC URL override (skips config rpc_endpoints)")
	dryRun := flag.Bool("dry-run", false, "verify everything but do not broadcast")
	flag.Parse()

	if *configPath == "" || *expect == "" || *token == "" || *to == "" || *amount <= 0 {
		log.Fatal("required: -config -expect -token -to -amount")
	}

	raw, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("reading config: %v", err)
	}
	var cfg struct {
		Mnemonic     string                         `json:"mnemonic"`
		RPCEndpoints map[string]config.EndpointList `json:"rpc_endpoints"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Fatalf("parsing config: %v", err)
	}
	rpcURL := *rpcOverride
	if rpcURL == "" {
		urls := cfg.RPCEndpoints[*chain]
		if len(urls) == 0 {
			log.Fatalf("no rpc endpoint for chain %q", *chain)
		}
		rpcURL = urls[0]
	}

	key, err := wallet.DeriveKey(cfg.Mnemonic, uint32(*index))
	if err != nil {
		log.Fatalf("deriving key: %v", err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	if !strings.EqualFold(from.Hex(), *expect) {
		log.Fatalf("derived address %s does not match expected %s — aborting", from.Hex(), *expect)
	}
	log.Printf("derived index %d → %s (matches expected)", *index, from.Hex())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rpc, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("dialing rpc: %v", err)
	}
	chainID, err := rpc.ChainID(ctx)
	if err != nil {
		log.Fatalf("getting chain id: %v", err)
	}

	parsed, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		log.Fatalf("parsing abi: %v", err)
	}

	tokenAddr := common.HexToAddress(*token)
	toAddr := common.HexToAddress(*to)
	amt := big.NewInt(*amount)

	balData, _ := parsed.Pack("balanceOf", from)
	balRaw, err := rpc.CallContract(ctx, ethereum.CallMsg{To: &tokenAddr, Data: balData}, nil)
	if err != nil {
		log.Fatalf("checking token balance: %v", err)
	}
	bal := new(big.Int).SetBytes(balRaw)
	log.Printf("token balance: %s (need %s)", bal, amt)
	if bal.Cmp(amt) < 0 {
		log.Fatalf("insufficient token balance")
	}

	nonce, err := rpc.PendingNonceAt(ctx, from)
	if err != nil {
		log.Fatalf("getting nonce: %v", err)
	}
	log.Printf("chainID=%s nonce=%d sending %s units of %s → %s", chainID, nonce, amt, tokenAddr.Hex(), toAddr.Hex())

	if *dryRun {
		log.Printf("dry-run: not broadcasting")
		return
	}

	data, err := parsed.Pack("transfer", toAddr, amt)
	if err != nil {
		log.Fatalf("packing transfer: %v", err)
	}

	signedTx, err := swaps.SignAndBroadcast(ctx, rpc, chainID, key, tokenAddr, big.NewInt(0), 100000, data, "Manual USDC transfer")
	if err != nil {
		log.Fatalf("broadcast failed: %v (check on-chain before retrying — tx may have landed anyway)", err)
	}
	fmt.Printf("TXHASH=%s\n", signedTx.Hash().Hex())

	for i := 0; i < 30; i++ {
		receipt, err := rpc.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil {
			fmt.Printf("MINED block=%s status=%d\n", receipt.BlockNumber, receipt.Status)
			if receipt.Status != 1 {
				os.Exit(1)
			}
			return
		}
		time.Sleep(2 * time.Second)
	}
	log.Printf("no receipt after 60s — check explorer for %s", signedTx.Hash().Hex())
}
