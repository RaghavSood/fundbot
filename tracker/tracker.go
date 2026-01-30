package tracker

import (
	"context"
	"fmt"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/swaps"
)

type Tracker struct {
	store   *db.Store
	swapMgr *swaps.Manager
	botAPI  *tgbotapi.BotAPI
}

func New(store *db.Store, swapMgr *swaps.Manager, botAPI *tgbotapi.BotAPI) *Tracker {
	return &Tracker{
		store:   store,
		swapMgr: swapMgr,
		botAPI:  botAPI,
	}
}

func (t *Tracker) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
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

	for _, topup := range pending {
		status, err := t.swapMgr.CheckStatus(ctx, topup.Provider, topup.TxHash)
		if err != nil {
			log.Printf("Tracker: error checking %s: %v", topup.ShortID, err)
			continue
		}

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

func (t *Tracker) notifyUser(topup db.Topup) {
	text := fmt.Sprintf("*Topup %s Complete*\nYour swap has been completed successfully.\nTx: `%s`",
		topup.ShortID, topup.TxHash)

	msg := tgbotapi.NewMessage(topup.UserID, text)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	if _, err := t.botAPI.Send(msg); err != nil {
		log.Printf("Tracker: error notifying user %d: %v", topup.UserID, err)
	}
}
