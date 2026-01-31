package tracker

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/cowswap"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/swaps"
)

type Tracker struct {
	cfg       *config.Config
	store     *db.Store
	swapMgr   *swaps.Manager
	cowClient *cowswap.Client
	botAPI    *tgbotapi.BotAPI
}

func New(cfg *config.Config, store *db.Store, swapMgr *swaps.Manager, cowClient *cowswap.Client, botAPI *tgbotapi.BotAPI) *Tracker {
	return &Tracker{
		cfg:       cfg,
		store:     store,
		swapMgr:   swapMgr,
		cowClient: cowClient,
		botAPI:    botAPI,
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
	t.pollTopups(ctx)
	t.pollGasRefills(ctx)
}

func (t *Tracker) pollTopups(ctx context.Context) {
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

		switch status {
		case "completed":
			if err := t.store.UpdateTopupStatus(ctx, db.UpdateTopupStatusParams{
				Status: "completed",
				ID:     topup.ID,
			}); err != nil {
				log.Printf("Tracker: error updating %s: %v", topup.ShortID, err)
				continue
			}
			log.Printf("Tracker: topup %s completed", topup.ShortID)
			t.notifyUser(topup, "completed")
		case "failed":
			if err := t.store.UpdateTopupStatus(ctx, db.UpdateTopupStatusParams{
				Status: "failed",
				ID:     topup.ID,
			}); err != nil {
				log.Printf("Tracker: error updating %s: %v", topup.ShortID, err)
				continue
			}
			log.Printf("Tracker: topup %s failed", topup.ShortID)
			t.notifyUser(topup, "failed")
		}
	}
}

func (t *Tracker) pollGasRefills(ctx context.Context) {
	if t.cowClient == nil {
		return
	}

	pending, err := t.store.ListPendingGasRefills(ctx)
	if err != nil {
		log.Printf("Tracker: error listing pending gas refills: %v", err)
		return
	}

	if len(pending) == 0 {
		return
	}

	log.Printf("Tracker: checking %d pending gas refill(s)", len(pending))

	for _, refill := range pending {
		select {
		case <-ctx.Done():
			return
		default:
		}

		status, err := t.cowClient.CheckOrderStatus(refill.Chain, refill.OrderUid)
		if err != nil {
			log.Printf("Tracker: error checking gas refill %d: %v", refill.ID, err)
			continue
		}

		log.Printf("Tracker: gas refill %d (%s) status = %s", refill.ID, refill.Chain, status)

		var newStatus string
		switch status {
		case "fulfilled":
			newStatus = "fulfilled"
		case "expired", "cancelled":
			newStatus = status
		default:
			continue // still open/pending
		}

		if err := t.store.UpdateGasRefillStatus(ctx, db.UpdateGasRefillStatusParams{
			Status: newStatus,
			ID:     refill.ID,
		}); err != nil {
			log.Printf("Tracker: error updating gas refill %d: %v", refill.ID, err)
			continue
		}

		t.notifyGasRefill(refill, newStatus)
	}
}

func (t *Tracker) notifyUser(topup db.ListPendingTopupsRow, status string) {
	explorerURL := t.cfg.ExplorerTxURL(topup.FromChain, topup.TxHash)
	var text string
	switch status {
	case "completed":
		text = fmt.Sprintf("*Topup %s Complete*\nYour swap has been completed successfully.\nTx: `%s`\n[View on Explorer](%s)",
			topup.ShortID, topup.TxHash, explorerURL)
	case "failed":
		text = fmt.Sprintf("*Topup %s Failed*\nYour swap has failed. Funds may be refunded automatically.\nTx: `%s`\n[View on Explorer](%s)",
			topup.ShortID, topup.TxHash, explorerURL)
	default:
		return
	}

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

func (t *Tracker) notifyGasRefill(refill db.GasRefill, status string) {
	symbol := strings.ToUpper(refill.Chain)
	if refill.Chain == "avalanche" {
		symbol = "AVAX"
	} else if refill.Chain == "base" {
		symbol = "ETH"
	}

	explorerURL := fmt.Sprintf("https://explorer.cow.fi/orders/%s", refill.OrderUid)

	var text string
	switch status {
	case "fulfilled":
		text = fmt.Sprintf("Gas refill on %s completed. USDC → %s swap filled.\n[View Order](%s)", symbol, symbol, explorerURL)
	case "expired":
		text = fmt.Sprintf("Gas refill order on %s expired unfilled. It will be retried next time you check /balance.\n[View Order](%s)", symbol, explorerURL)
	case "cancelled":
		text = fmt.Sprintf("Gas refill order on %s was cancelled. It will be retried next time you check /balance.\n[View Order](%s)", symbol, explorerURL)
	}

	chatID := refill.ChatID
	if chatID == 0 {
		chatID = refill.UserID
	}
	if chatID == 0 {
		return // no one to notify
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	if _, err := t.botAPI.Send(msg); err != nil {
		log.Printf("Tracker: error notifying gas refill to %d: %v", chatID, err)
	}
}
