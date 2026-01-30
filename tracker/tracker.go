package tracker

import (
	"context"
	"fmt"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/swaps"
)

type Tracker struct {
	cfg     *config.Config
	store   *db.Store
	swapMgr *swaps.Manager
	botAPI  *tgbotapi.BotAPI
}

func New(cfg *config.Config, store *db.Store, swapMgr *swaps.Manager, botAPI *tgbotapi.BotAPI) *Tracker {
	return &Tracker{
		cfg:     cfg,
		store:   store,
		swapMgr: swapMgr,
		botAPI:  botAPI,
	}
}

func (t *Tracker) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Run once immediately on start
	t.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("Tracker stopped")
			return
		case <-ticker.C:
			t.poll(ctx)
		}
	}
}

func (t *Tracker) poll(ctx context.Context) {
	pending, err := t.store.ListPendingTopups(ctx)
	if err != nil {
		log.Printf("Tracker: error listing pending topups: %v", err)
		return
	}

	if len(pending) == 0 {
		return
	}

	log.Printf("Tracker: checking %d pending topup(s)", len(pending))

	for _, topup := range pending {
		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("Tracker: checking %s (tx %s)", topup.ShortID, topup.TxHash)

		status, err := t.swapMgr.CheckStatus(ctx, topup.Provider, topup.TxHash, topup.ExternalID)
		if err != nil {
			log.Printf("Tracker: error checking %s: %v", topup.ShortID, err)
			continue
		}

		log.Printf("Tracker: %s status = %s", topup.ShortID, status)

		if status == "completed" {
			if err := t.store.UpdateTopupStatus(ctx, db.UpdateTopupStatusParams{
				Status: "completed",
				ID:     topup.ID,
			}); err != nil {
				log.Printf("Tracker: error updating %s: %v", topup.ShortID, err)
				continue
			}

			log.Printf("Tracker: topup %s completed", topup.ShortID)
			t.notifyUser(topup)
		}
	}
}

func (t *Tracker) notifyUser(topup db.ListPendingTopupsRow) {
	explorerURL := t.cfg.ExplorerTxURL(topup.FromChain, topup.TxHash)
	text := fmt.Sprintf("*Topup %s Complete*\nYour swap has been completed successfully.\nTx: `%s`\n[View on Explorer](%s)",
		topup.ShortID, topup.TxHash, explorerURL)

	// Notify the chat where the topup was initiated; fall back to user DM for legacy topups.
	chatID := topup.ChatID
	if chatID == 0 {
		chatID = topup.UserID
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	if _, err := t.botAPI.Send(msg); err != nil {
		log.Printf("Tracker: error notifying chat %d: %v", chatID, err)
	}
}
