// Command intentstest exercises the service-level Near Intents account:
// authentication, balances, dry quotes on the confidential rail, deposits
// from the treasury EVM wallet, shielding, and confidential swaps.
//
// Usage:
//
//	intentstest -config config.json info
//	intentstest -config config.json dryquote
//	intentstest -config config.json deposit -chain base -usd 25
//	intentstest -config config.json shield -chain base -usd 20
//	intentstest -config config.json swap -dest SOL.SOL -to <address> -usd 2
//	intentstest -config config.json status -addr <depositAddress>
//	intentstest -config config.json history
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/intents"
	"github.com/RaghavSood/fundbot/nearintents"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/thorchain"
	"github.com/RaghavSood/fundbot/wallet"
)

const erc20TransferABI = `[{"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"}]`

var chainIDs = map[string]*big.Int{
	"avalanche": big.NewInt(43114),
	"base":      big.NewInt(8453),
}

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatal("usage: intentstest -config config.json <info|dryquote|deposit|shield|swap|status|history> [flags]")
	}
	action := flag.Arg(0)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	apiKey := cfg.Providers["nearintents"].APIKey
	if apiKey == "" {
		log.Fatal("providers.nearintents.api_key is required")
	}

	key, err := wallet.DeriveTreasuryKey(cfg.Mnemonic)
	if err != nil {
		log.Fatalf("deriving treasury key: %v", err)
	}

	client := intents.NewClient(apiKey, key, nil)
	ctx := context.Background()

	sub := flag.NewFlagSet(action, flag.ExitOnError)
	chain := sub.String("chain", "base", "source chain (base|avalanche)")
	usd := sub.Float64("usd", 1, "USD amount")
	dest := sub.String("dest", "SOL.SOL", "destination asset (CHAIN.SYMBOL)")
	to := sub.String("to", "", "destination address")
	addr := sub.String("addr", "", "deposit address for status")
	_ = sub.Parse(flag.Args()[1:])

	switch action {
	case "info":
		runInfo(ctx, client)
	case "dryquote":
		runDryQuote(ctx, client, *chain, *dest, *usd)
	case "deposit":
		runDeposit(ctx, cfg, client, *chain, *usd)
	case "shield":
		runShield(ctx, client, *chain, *usd)
	case "swap":
		runSwap(ctx, client, *chain, *dest, *to, *usd)
	case "status":
		runStatus(ctx, client, *addr)
	case "history":
		runHistory(ctx, client)
	default:
		log.Fatalf("unknown action %q", action)
	}
}

func usdcAmount(usd float64) string {
	return fmt.Sprintf("%d", int64(usd*1e6))
}

func sourceToken(chain string) string {
	id, ok := nearintents.SourceTokenID(chain)
	if !ok {
		log.Fatalf("unknown source chain %q", chain)
	}
	return id
}

func destToken(dest string) string {
	asset, err := swaps.ParseAsset(dest)
	if err != nil {
		log.Fatalf("parsing dest asset: %v", err)
	}
	id, ok := nearintents.AssetToTokenID(asset)
	if !ok {
		log.Fatalf("no near intents token ID for %s", dest)
	}
	return id
}

func runInfo(ctx context.Context, client *intents.Client) {
	fmt.Printf("Service account (signer_id): %s\n\n", client.SignerID())

	fmt.Println("== Confidential balances (via /v0/account/balances) ==")
	bals, err := client.ConfidentialBalances(ctx)
	if err != nil {
		fmt.Printf("  ERROR: %v\n", err)
	} else if len(bals) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, b := range bals {
			fmt.Printf("  %-70s %s (%s)\n", b.TokenID, b.Available, b.Source)
		}
	}

	fmt.Println("\n== Public (Main) intents balances (via NEAR RPC) ==")
	tokenIDs := []string{sourceToken("base"), sourceToken("avalanche")}
	pub, err := client.PublicBalances(ctx, tokenIDs)
	if err != nil {
		fmt.Printf("  ERROR: %v\n", err)
	} else {
		for i, id := range tokenIDs {
			fmt.Printf("  %-70s %s\n", id, pub[i])
		}
	}
}

func runDryQuote(ctx context.Context, client *intents.Client, chain, dest string, usd float64) {
	origin := sourceToken(chain)
	target := destToken(dest)

	for _, depositType := range []string{intents.TypeIntents, intents.TypeConfidentialIntents} {
		fmt.Printf("== Dry quote: %s USDC(%s) -> %s, depositType=%s ==\n", usdcAmount(usd), chain, dest, depositType)
		q, err := client.Quote(ctx, intents.QuoteParams{
			Dry:              true,
			OriginAsset:      origin,
			DestinationAsset: target,
			Amount:           usdcAmount(usd),
			DepositType:      depositType,
			Recipient:        "11111111111111111111111111111111", // placeholder (SOL system program)
			RecipientType:    intents.TypeDestinationChain,
			RefundTo:         client.SignerID(),
			RefundType:       depositType,
		})
		if err != nil {
			fmt.Printf("  ERROR: %v\n\n", err)
			continue
		}
		fmt.Printf("  amountOut=%s (%s, $%s) minAmountOut=%s eta=%.0fs\n\n",
			q.AmountOut, q.AmountOutFmt, q.AmountOutUsd, q.MinAmountOut, q.TimeEstimate)
	}
}

// runDeposit moves USDC from the treasury EVM wallet into the service's
// public intents balance via a 1-Click deposit address.
func runDeposit(ctx context.Context, cfg *config.Config, client *intents.Client, chain string, usd float64) {
	ecdsaKey, err := wallet.DeriveTreasuryKey(cfg.Mnemonic)
	if err != nil {
		log.Fatalf("deriving treasury key: %v", err)
	}
	origin := sourceToken(chain)

	q, err := client.Quote(ctx, intents.QuoteParams{
		Dry:              false,
		OriginAsset:      origin,
		DestinationAsset: origin, // same asset: deposit, not swap
		Amount:           usdcAmount(usd),
		DepositType:      intents.TypeOriginChain,
		Recipient:        client.SignerID(),
		RecipientType:    intents.TypeIntents,
		RefundTo:         strings.ToLower(crypto.PubkeyToAddress(ecdsaKey.PublicKey).Hex()),
		RefundType:       intents.TypeOriginChain,
	})
	if err != nil {
		log.Fatalf("deposit quote: %v", err)
	}
	fmt.Printf("Deposit address on %s: %s (amountOut=%s)\n", chain, q.DepositAddress, q.AmountOutFmt)

	rpcURL := cfg.RPCEndpoints[chain]
	if rpcURL == "" {
		log.Fatalf("no RPC endpoint configured for %s", chain)
	}
	rpc, err := ethclient.Dial(rpcURL)
	if err != nil {
		log.Fatalf("dialing RPC: %v", err)
	}

	parsed, err := abi.JSON(strings.NewReader(erc20TransferABI))
	if err != nil {
		log.Fatal(err)
	}
	amount := new(big.Int).SetInt64(int64(usd * 1e6))
	data, err := parsed.Pack("transfer", common.HexToAddress(q.DepositAddress), amount)
	if err != nil {
		log.Fatal(err)
	}

	tx, err := swaps.SignAndBroadcast(ctx, rpc, chainIDs[chain], ecdsaKey, thorchain.USDCContracts[chain], big.NewInt(0), 100000, data, "treasury intents deposit")
	if err != nil {
		log.Fatalf("sending deposit: %v", err)
	}
	fmt.Printf("Sent: %s\n", tx.Hash().Hex())

	if err := client.SubmitDepositTx(ctx, tx.Hash().Hex(), q.DepositAddress); err != nil {
		fmt.Printf("submit deposit tx (non-fatal): %v\n", err)
	}
	fmt.Printf("Poll with: intentstest status -addr %s\n", q.DepositAddress)
}

// runShield moves USDC from the public intents balance to the confidential balance.
func runShield(ctx context.Context, client *intents.Client, chain string, usd float64) {
	origin := sourceToken(chain)

	q, err := client.Quote(ctx, intents.QuoteParams{
		Dry:              false,
		OriginAsset:      origin,
		DestinationAsset: origin,
		Amount:           usdcAmount(usd),
		DepositType:      intents.TypeIntents,
		Recipient:        client.SignerID(),
		RecipientType:    intents.TypeConfidentialIntents,
		RefundTo:         client.SignerID(),
		RefundType:       intents.TypeIntents,
	})
	if err != nil {
		log.Fatalf("shield quote: %v", err)
	}
	fmt.Printf("Shield quote: depositAddress=%s amountOut=%s\n", q.DepositAddress, q.AmountOutFmt)

	hash, err := client.ExecuteFromBalance(ctx, q.DepositAddress)
	if err != nil {
		log.Fatalf("executing shield: %v", err)
	}
	fmt.Printf("Intent submitted: %s\n", hash)
	fmt.Printf("Poll with: intentstest status -addr %s\n", q.DepositAddress)
}

// runSwap performs a confidential swap from the confidential balance to an
// external destination address.
func runSwap(ctx context.Context, client *intents.Client, chain, dest, to string, usd float64) {
	if to == "" {
		log.Fatal("-to destination address is required")
	}
	origin := sourceToken(chain)
	target := destToken(dest)

	q, err := client.Quote(ctx, intents.QuoteParams{
		Dry:              false,
		OriginAsset:      origin,
		DestinationAsset: target,
		Amount:           usdcAmount(usd),
		DepositType:      intents.TypeConfidentialIntents,
		Recipient:        to,
		RecipientType:    intents.TypeDestinationChain,
		RefundTo:         client.SignerID(),
		RefundType:       intents.TypeConfidentialIntents,
	})
	if err != nil {
		log.Fatalf("swap quote: %v", err)
	}
	fmt.Printf("Swap quote: depositAddress=%s amountOut=%s ($%s) eta=%.0fs\n", q.DepositAddress, q.AmountOutFmt, q.AmountOutUsd, q.TimeEstimate)

	hash, err := client.ExecuteFromBalance(ctx, q.DepositAddress)
	if err != nil {
		log.Fatalf("executing swap: %v", err)
	}
	fmt.Printf("Intent submitted: %s\n", hash)
	fmt.Printf("Poll with: intentstest status -addr %s\n", q.DepositAddress)
}

func runStatus(ctx context.Context, client *intents.Client, addr string) {
	if addr == "" {
		log.Fatal("-addr is required")
	}
	for {
		status, err := client.Status(ctx, addr)
		if err != nil {
			log.Fatalf("status: %v", err)
		}
		fmt.Printf("%s %s\n", time.Now().Format(time.TimeOnly), status)
		if status == "SUCCESS" || status == "FAILED" || status == "REFUNDED" {
			return
		}
		time.Sleep(5 * time.Second)
	}
}

func runHistory(ctx context.Context, client *intents.Client) {
	items, _, err := client.History(ctx, "", 20)
	if err != nil {
		log.Fatalf("history: %v", err)
	}
	if len(items) == 0 {
		fmt.Println("(no history)")
		return
	}
	for _, it := range items {
		fmt.Printf("%s  %-10s %s->%s  in=%s ($%s) out=%s ($%s)  recipient=%s\n",
			it.CreatedAt, it.Status, it.DepositType, it.RecipientType,
			it.AmountInFormatted, it.AmountInUsd, it.AmountOutFormatted, it.AmountOutUsd, it.Recipient)
	}
	_ = os.Stdout.Sync()
}
