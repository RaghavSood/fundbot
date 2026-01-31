package bot

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/balances"
	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/cowswap"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/thorchain"
	"github.com/RaghavSood/fundbot/version"
	"github.com/RaghavSood/fundbot/wallet"
)

type Bot struct {
	api        *tgbotapi.BotAPI
	config     *config.Config
	db         *db.Store
	swapMgr    *swaps.Manager
	rpcClients map[string]*ethclient.Client
	cowClient  *cowswap.Client
}

func New(cfg *config.Config, store *db.Store, swapMgr *swaps.Manager, rpcClients map[string]*ethclient.Client, cowClient *cowswap.Client) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("creating bot API: %w", err)
	}

	log.Printf("Authorized on account %s", api.Self.UserName)
	return &Bot{
		api:        api,
		config:     cfg,
		db:         store,
		swapMgr:    swapMgr,
		rpcClients: rpcClients,
		cowClient:  cowClient,
	}, nil
}

func (b *Bot) BotAPI() *tgbotapi.BotAPI {
	return b.api
}

func (b *Bot) Run() error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		msg := update.Message
		isGroup := !msg.Chat.IsPrivate()

		if isGroup && b.config.Mode == config.ModeSingle {
			b.reply(msg, "Group chats are not supported in single mode.")
			continue
		}

		// In group chats (multi mode), all users are authorized.
		// In DMs, check the whitelist/admin.
		if !isGroup && !b.config.IsAuthorized(msg.From.ID) {
			b.reply(msg, "You are not authorized to use this bot.")
			continue
		}

		b.handleMessage(msg)
	}

	return nil
}

func (b *Bot) Stop() {
	b.api.StopReceivingUpdates()
}

func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	if !msg.IsCommand() {
		return
	}

	switch msg.Command() {
	case "start":
		b.handleStart(msg)
	case "address":
		b.handleAddress(msg)
	case "quote":
		b.handleQuote(msg)
	case "topup":
		b.handleTopup(msg)
	case "status":
		b.handleStatus(msg)
	case "balance", "balances":
		b.handleBalance(msg)
	case "help":
		b.handleStart(msg)
	case "version":
		b.reply(msg, fmt.Sprintf("`%s`", version.Version))
		return
	default:
		b.reply(msg, "Unknown command. Use /start to get started.")
	}
}

// Minimum native balance thresholds (~$1 worth of gas token).
// Conservative estimates to avoid unnecessary refills.
var minNativeWei = map[string]*big.Int{
	"base":      new(big.Int).Mul(big.NewInt(4), big.NewInt(1e14)), // 0.0004 ETH (~$1 at $2500)
	"avalanche": new(big.Int).Mul(big.NewInt(4), big.NewInt(1e16)), // 0.04 AVAX (~$1 at $25)
}

// refillUSDC is $5 USDC in smallest units (6 decimals).
var refillUSDC = big.NewInt(5_000_000)

func (b *Bot) handleBalance(msg *tgbotapi.Message) {
	index, err := b.walletIndex(msg)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v", err))
		return
	}

	addr, err := wallet.DeriveAddress(b.config.Mnemonic, index)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error deriving address: %v", err))
		return
	}

	ctx := context.Background()
	bals, err := balances.FetchBalances(ctx, b.rpcClients, []common.Address{addr}, thorchain.USDCContracts)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error fetching balances: %v", err))
		return
	}

	if len(bals) == 0 {
		b.reply(msg, "No balances found.")
		return
	}

	text := fmt.Sprintf("*Balances for* `%s`\n", addr.Hex())
	for _, bal := range bals {
		native := formatWei(bal.NativeBalance, bal.Chain)
		usdc := formatUSDC(bal.USDCBalance)
		text += fmt.Sprintf("\n*%s*\n  %s\n  %s USDC", chainLabel(bal.Chain), native, usdc)
	}
	b.reply(msg, text)

	// Check if any chain needs a gas refill (USDC → native token via CoWSwap)
	if b.cowClient == nil {
		return
	}

	privateKey, err := wallet.DeriveKey(b.config.Mnemonic, index)
	if err != nil {
		log.Printf("Error deriving key for gas refill: %v", err)
		return
	}

	for _, bal := range bals {
		threshold, ok := minNativeWei[bal.Chain]
		if !ok {
			continue
		}

		nativeBal := new(big.Int)
		nativeBal.SetString(bal.NativeBalance, 10)

		usdcBal := new(big.Int)
		usdcBal.SetString(bal.USDCBalance, 10)

		result, err := b.cowClient.RefillGasIfNeeded(ctx, bal.Chain, addr, privateKey, nativeBal, usdcBal, threshold, refillUSDC)
		if err != nil {
			log.Printf("Gas refill error on %s: %v", bal.Chain, err)
			b.reply(msg, fmt.Sprintf("Gas refill error on %s: %v", chainLabel(bal.Chain), err))
			continue
		}
		if result != nil {
			// Store gas refill for tracking
			_, err := b.db.InsertGasRefill(ctx, db.InsertGasRefillParams{
				Chain:         result.Chain,
				OrderUid:      result.OrderUID,
				WalletAddress: addr.Hex(),
				SellAmount:    result.SellAmount,
				BuyAmount:     result.BuyAmount,
				Status:        "open",
				UserID:        msg.From.ID,
				ChatID:        msg.Chat.ID,
			})
			if err != nil {
				log.Printf("Error storing gas refill record: %v", err)
			}

			b.reply(msg, fmt.Sprintf("Low %s balance detected. Swapping $5 USDC → %s via CoWSwap (3m expiry).\n[View Order](https://explorer.cow.fi/orders/%s)",
				nativeSymbol(bal.Chain), nativeSymbol(bal.Chain), result.OrderUID))
		}
	}
}

func formatWei(wei string, chain string) string {
	val := new(big.Int)
	val.SetString(wei, 10)
	whole := new(big.Int).Div(val, big.NewInt(1e18))
	frac := new(big.Int).Mod(val, big.NewInt(1e18))
	fracStr := fmt.Sprintf("%018s", frac.String())[:6]
	return fmt.Sprintf("%s.%s %s", whole, fracStr, nativeSymbol(chain))
}

func formatUSDC(raw string) string {
	val := new(big.Int)
	val.SetString(raw, 10)
	whole := new(big.Int).Div(val, big.NewInt(1e6))
	frac := new(big.Int).Mod(val, big.NewInt(1e6))
	fracStr := fmt.Sprintf("%06s", frac.String())[:2]
	return fmt.Sprintf("%s.%s", whole, fracStr)
}

func nativeSymbol(chain string) string {
	switch chain {
	case "avalanche":
		return "AVAX"
	case "base":
		return "ETH"
	default:
		return strings.ToUpper(chain)
	}
}

func chainLabel(chain string) string {
	switch chain {
	case "avalanche":
		return "Avalanche"
	case "base":
		return "Base"
	default:
		return strings.Title(chain)
	}
}

func (b *Bot) handleStart(msg *tgbotapi.Message) {
	text := "Welcome to FundBot!\n\n" +
		"*Commands:*\n" +
		"/address - Show your wallet address\n" +
		"/balance - Show wallet balances\n" +
		"/quote `<address> <amount> <CHAIN.ASSET>` - Get a swap quote\n" +
		"/topup `<address> <amount> <CHAIN.ASSET>` - Execute a swap\n" +
		"/status `<topup_id>` - Check topup status\n\n" +
		"*Asset examples:*\n" +
		"`BTC.BTC`, `ETH.ETH`, `ETH.LINK-0x514910771AF9Ca656af840dff83E8264EcF986CA`"
	b.reply(msg, text)
}

func (b *Bot) handleAddress(msg *tgbotapi.Message) {
	index, err := b.walletIndex(msg)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v", err))
		return
	}

	addr, err := wallet.DeriveAddress(b.config.Mnemonic, index)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error deriving address: %v", err))
		return
	}

	b.reply(msg, fmt.Sprintf("Your wallet address: `%s`", addr.Hex()))
}

// parseSwapArgs parses "<address> <amount> <CHAIN.ASSET>" from command arguments
func parseSwapArgs(args string) (destination string, usdAmount float64, asset swaps.Asset, err error) {
	fields := strings.Fields(args)
	if len(fields) != 3 {
		err = fmt.Errorf("usage: <address> <amount> <CHAIN.ASSET>")
		return
	}

	destination = fields[0]

	usdAmount, err = strconv.ParseFloat(fields[1], 64)
	if err != nil {
		err = fmt.Errorf("invalid amount: %v", err)
		return
	}
	if usdAmount <= 0 {
		err = fmt.Errorf("amount must be positive")
		return
	}

	asset, err = swaps.ParseAsset(fields[2])
	if err != nil {
		err = fmt.Errorf("invalid asset: %v", err)
		return
	}

	return
}

func (b *Bot) insertQuote(ctx context.Context, quote *swaps.Quote, userID int64, chatID int64, destination string) (int64, error) {
	return b.db.InsertQuote(ctx, db.InsertQuoteParams{
		Type:           "fast",
		Provider:       quote.Provider,
		UserID:         userID,
		FromAsset:      quote.FromAsset.String(),
		FromChain:      quote.FromChain,
		ToAsset:        quote.ToAsset.String(),
		Destination:    destination,
		InputAmountUsd: quote.InputAmountUSD,
		InputAmount:    quote.InputAmount.String(),
		ExpectedOutput: quote.ExpectedOutput,
		Memo:           quote.Memo,
		Router:         quote.Router,
		VaultAddress:   quote.VaultAddress,
		Expiry:         quote.Expiry,
		ChatID:         chatID,
	})
}

func (b *Bot) handleQuote(msg *tgbotapi.Message) {
	destination, usdAmount, asset, err := parseSwapArgs(msg.CommandArguments())
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v\nUsage: /quote <address> <amount> <CHAIN.ASSET>", err))
		return
	}

	index, err := b.walletIndex(msg)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v", err))
		return
	}
	senderAddr, err := wallet.DeriveAddress(b.config.Mnemonic, index)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error deriving address: %v", err))
		return
	}

	b.reply(msg, fmt.Sprintf("Fetching quote for $%.2f → %s to %s...", usdAmount, asset, destination))

	ctx := context.Background()
	quote, err := b.swapMgr.BestQuote(ctx, asset, usdAmount, destination, senderAddr)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Quote error: %v", err))
		return
	}

	quoteID, err := b.insertQuote(ctx, quote, msg.From.ID, msg.Chat.ID, destination)
	if err != nil {
		log.Printf("Error storing quote: %v", err)
	}

	text := fmt.Sprintf("*Quote #%d*\nProvider: %s\nSource: %s (%s)\nInput: $%.2f USDC\nExpected output: %s (raw units)\nMemo: `%s`",
		quoteID, quote.Provider, quote.FromAsset, quote.FromChain,
		quote.InputAmountUSD, quote.ExpectedOutput, quote.Memo)
	b.reply(msg, text)
}

func (b *Bot) handleTopup(msg *tgbotapi.Message) {
	destination, usdAmount, asset, err := parseSwapArgs(msg.CommandArguments())
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v\nUsage: /topup <address> <amount> <CHAIN.ASSET>", err))
		return
	}

	// Derive key early — needed for both balance check and execution
	index, err := b.walletIndex(msg)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v", err))
		return
	}
	privateKey, err := wallet.DeriveKey(b.config.Mnemonic, index)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error deriving key: %v", err))
		return
	}
	senderAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	b.reply(msg, fmt.Sprintf("Executing swap: $%.2f → %s to %s...", usdAmount, asset, destination))

	ctx := context.Background()
	quote, err := b.swapMgr.BestQuote(ctx, asset, usdAmount, destination, senderAddr)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Quote error: %v", err))
		return
	}

	quoteID, err := b.insertQuote(ctx, quote, msg.From.ID, msg.Chat.ID, destination)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error storing quote: %v", err))
		return
	}

	result, err := b.swapMgr.ExecuteSwap(ctx, quote, privateKey)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Swap execution failed: %v", err))
		return
	}

	// Store topup
	topupRow, err := b.db.InsertTopupWithShortID(ctx, db.InsertTopupParams{
		Type:       "fast",
		QuoteID:    quoteID,
		UserID:     msg.From.ID,
		Provider:   quote.Provider,
		FromChain:  quote.FromChain,
		TxHash:     result.TxHash,
		Status:     "pending",
		ChatID:     msg.Chat.ID,
		ExternalID: result.ExternalID,
	})
	if err != nil {
		log.Printf("Error storing topup: %v", err)
	}

	explorerURL := b.config.ExplorerTxURL(quote.FromChain, result.TxHash)
	text := fmt.Sprintf("*Topup %s*\nTx: `%s`\n[Explorer](%s)\nUse /status %s to check progress.",
		topupRow.ShortID, result.TxHash, explorerURL, topupRow.ShortID)
	b.reply(msg, text)
}

func (b *Bot) handleStatus(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		b.reply(msg, "Usage: /status <topup_id>")
		return
	}

	ctx := context.Background()
	topup, err := b.db.GetTopupByShortID(ctx, args)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Topup not found: %v", err))
		return
	}

	explorerURL := b.config.ExplorerTxURL(topup.FromChain, topup.TxHash)
	text := fmt.Sprintf("*Topup %s*\nProvider: %s\nChain: %s\nTx: `%s`\nStatus: %s\n[Explorer](%s)",
		topup.ShortID, topup.Provider, topup.FromChain, topup.TxHash, topup.Status, explorerURL)
	b.reply(msg, text)
}

// walletIndex returns the BIP44 derivation index for a message context.
// Single mode: always 0. Multi mode: address_assignments row ID.
func (b *Bot) walletIndex(msg *tgbotapi.Message) (uint32, error) {
	if b.config.Mode == config.ModeSingle {
		return 0, nil
	}

	ctx := context.Background()

	var assignedToID int64
	var assignedToType string

	if msg.Chat.IsPrivate() {
		user, err := b.db.GetOrCreateUser(ctx, msg.From.ID, msg.From.UserName)
		if err != nil {
			return 0, err
		}
		assignedToID = user.ID
		assignedToType = "user"
	} else {
		chat, err := b.db.GetOrCreateChat(ctx, msg.Chat.ID, msg.Chat.Title)
		if err != nil {
			return 0, err
		}
		assignedToID = chat.ID
		assignedToType = "chat"
	}

	assignment, err := b.db.GetOrCreateAddressAssignment(ctx, assignedToID, assignedToType)
	if err != nil {
		return 0, fmt.Errorf("address assignment: %w", err)
	}
	return uint32(assignment.ID), nil
}

func (b *Bot) reply(msg *tgbotapi.Message, text string) {
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ReplyToMessageID = msg.MessageID
	reply.ParseMode = "Markdown"
	reply.DisableWebPagePreview = true
	if _, err := b.api.Send(reply); err != nil {
		log.Printf("Error sending markdown message, retrying as plain text: %v", err)
		reply.ParseMode = ""
		if _, err := b.api.Send(reply); err != nil {
			log.Printf("Error sending plain text message: %v", err)
		}
	}
}
