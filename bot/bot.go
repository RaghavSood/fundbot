package bot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/wallet"
)

type Bot struct {
	api     *tgbotapi.BotAPI
	config  *config.Config
	db      *db.DB
	swapMgr *swaps.Manager
}

func New(cfg *config.Config, database *db.DB, swapMgr *swaps.Manager) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("creating bot API: %w", err)
	}

	log.Printf("Authorized on account %s", api.Self.UserName)
	return &Bot{
		api:     api,
		config:  cfg,
		db:      database,
		swapMgr: swapMgr,
	}, nil
}

func (b *Bot) Run() error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		if !b.config.IsAuthorized(update.Message.From.ID) {
			b.reply(update.Message, "You are not authorized to use this bot.")
			continue
		}

		b.handleMessage(update.Message)
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
	default:
		b.reply(msg, "Unknown command. Use /start to get started.")
	}
}

func (b *Bot) handleStart(msg *tgbotapi.Message) {
	text := "Welcome to FundBot!\n\n" +
		"*Commands:*\n" +
		"/address - Show your wallet address\n" +
		"/quote `<address> <amount> <CHAIN.ASSET>` - Get a swap quote\n" +
		"/topup `<address> <amount> <CHAIN.ASSET>` - Execute a swap\n" +
		"/status `<topup_id>` - Check topup status\n\n" +
		"*Asset examples:*\n" +
		"`BTC.BTC`, `ETH.ETH`, `ETH.LINK-0x514910771AF9Ca656af840dff83E8264EcF986CA`"
	b.reply(msg, text)
}

func (b *Bot) handleAddress(msg *tgbotapi.Message) {
	index, err := b.walletIndex(msg.From.ID, msg.From.UserName)
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

func (b *Bot) handleQuote(msg *tgbotapi.Message) {
	destination, usdAmount, asset, err := parseSwapArgs(msg.CommandArguments())
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v\nUsage: /quote <address> <amount> <CHAIN.ASSET>", err))
		return
	}

	b.reply(msg, fmt.Sprintf("Fetching quote for $%.2f → %s to %s...", usdAmount, asset, destination))

	ctx := context.Background()
	quote, err := b.swapMgr.BestQuote(ctx, asset, usdAmount, destination)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Quote error: %v", err))
		return
	}

	// Store quote in DB
	quoteID, err := b.db.InsertQuote(&db.QuoteRecord{
		Type:           "fast",
		Provider:       quote.Provider,
		UserID:         msg.From.ID,
		FromAsset:      quote.FromAsset.String(),
		FromChain:      quote.FromChain,
		ToAsset:        quote.ToAsset.String(),
		Destination:    destination,
		InputAmountUSD: quote.InputAmountUSD,
		InputAmount:    quote.InputAmount.String(),
		ExpectedOutput: quote.ExpectedOutput,
		Memo:           quote.Memo,
		Router:         quote.Router,
		VaultAddress:   quote.VaultAddress,
		Expiry:         quote.Expiry,
	})
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

	b.reply(msg, fmt.Sprintf("Executing swap: $%.2f → %s to %s...", usdAmount, asset, destination))

	ctx := context.Background()
	quote, err := b.swapMgr.BestQuote(ctx, asset, usdAmount, destination)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Quote error: %v", err))
		return
	}

	// Store quote
	quoteID, err := b.db.InsertQuote(&db.QuoteRecord{
		Type:           "fast",
		Provider:       quote.Provider,
		UserID:         msg.From.ID,
		FromAsset:      quote.FromAsset.String(),
		FromChain:      quote.FromChain,
		ToAsset:        quote.ToAsset.String(),
		Destination:    destination,
		InputAmountUSD: quote.InputAmountUSD,
		InputAmount:    quote.InputAmount.String(),
		ExpectedOutput: quote.ExpectedOutput,
		Memo:           quote.Memo,
		Router:         quote.Router,
		VaultAddress:   quote.VaultAddress,
		Expiry:         quote.Expiry,
	})
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error storing quote: %v", err))
		return
	}

	// Derive key for execution
	index, err := b.walletIndex(msg.From.ID, msg.From.UserName)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error: %v", err))
		return
	}

	privateKey, err := wallet.DeriveKey(b.config.Mnemonic, index)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Error deriving key: %v", err))
		return
	}

	txHash, err := b.swapMgr.ExecuteSwap(ctx, quote, privateKey)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Swap execution failed: %v", err))
		return
	}

	// Store topup
	shortID, err := b.db.InsertTopup(&db.TopupRecord{
		Type:      "fast",
		QuoteID:   quoteID,
		UserID:    msg.From.ID,
		Provider:  quote.Provider,
		FromChain: quote.FromChain,
		TxHash:    txHash,
		Status:    "pending",
	})
	if err != nil {
		log.Printf("Error storing topup: %v", err)
	}

	trackerURL := fmt.Sprintf("https://thorchain.net/tx/%s", txHash)
	text := fmt.Sprintf("*Topup %s*\nTx: `%s`\nTracker: %s\nUse /status %s to check progress.", shortID, txHash, trackerURL, shortID)
	b.reply(msg, text)
}

func (b *Bot) handleStatus(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		b.reply(msg, "Usage: /status <topup_id>")
		return
	}

	topup, err := b.db.GetTopup(args)
	if err != nil {
		b.reply(msg, fmt.Sprintf("Topup not found: %v", err))
		return
	}

	trackerURL := fmt.Sprintf("https://thorchain.net/tx/%s", topup.TxHash)
	text := fmt.Sprintf("*Topup %s*\nProvider: %s\nChain: %s\nTx: `%s`\nStatus: %s\nTracker: %s",
		topup.ShortID, topup.Provider, topup.FromChain, topup.TxHash, topup.Status, trackerURL)
	b.reply(msg, text)
}

// walletIndex returns the BIP44 derivation index for a user.
// Single mode: always 0. Multi mode: SQLite row ID.
func (b *Bot) walletIndex(telegramID int64, username string) (uint32, error) {
	if b.config.Mode == config.ModeSingle {
		return 0, nil
	}

	user, err := b.db.GetOrCreateUser(telegramID, username)
	if err != nil {
		return 0, err
	}
	return uint32(user.ID), nil
}

func (b *Bot) reply(msg *tgbotapi.Message, text string) {
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ReplyToMessageID = msg.MessageID
	reply.ParseMode = "Markdown"
	reply.DisableWebPagePreview = true
	if _, err := b.api.Send(reply); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}
