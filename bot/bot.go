package bot

import (
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/wallet"
)

type Bot struct {
	api    *tgbotapi.BotAPI
	config *config.Config
	db     *db.DB // nil in single mode
}

func New(cfg *config.Config) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("creating bot API: %w", err)
	}

	b := &Bot{
		api:    api,
		config: cfg,
	}

	if cfg.Mode == config.ModeMulti {
		database, err := db.Open(cfg.DatabasePath)
		if err != nil {
			return nil, fmt.Errorf("opening database: %w", err)
		}
		b.db = database
	}

	log.Printf("Authorized on account %s", api.Self.UserName)
	return b, nil
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
	if b.db != nil {
		b.db.Close()
	}
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
	default:
		b.reply(msg, "Unknown command. Use /start to get started.")
	}
}

func (b *Bot) handleStart(msg *tgbotapi.Message) {
	b.reply(msg, "Welcome to FundBot! Use /address to see your wallet address.")
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
	if _, err := b.api.Send(reply); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}
