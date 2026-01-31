package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/RaghavSood/fundbot/bot"
	"github.com/RaghavSood/fundbot/config"
	"github.com/RaghavSood/fundbot/cowswap"
	"github.com/RaghavSood/fundbot/db"
	"github.com/RaghavSood/fundbot/server"
	"github.com/RaghavSood/fundbot/simpleswap"
	"github.com/RaghavSood/fundbot/swaps"
	"github.com/RaghavSood/fundbot/thorchain"
	"github.com/RaghavSood/fundbot/tracker"
)

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Open database (always needed now for quotes/topups tables)
	database, err := db.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Connect RPC clients
	rpcClients := make(map[string]*ethclient.Client)
	for name, url := range cfg.RPCEndpoints {
		client, err := ethclient.Dial(url)
		if err != nil {
			log.Fatalf("Failed to connect to %s RPC at %s: %v", name, url, err)
		}
		rpcClients[name] = client
		log.Printf("Connected to %s RPC", name)
	}

	// Initialize providers
	var providers []swaps.Provider
	tcProvider := thorchain.NewProvider(rpcClients)
	providers = append(providers, tcProvider)

	if ssCfg, ok := cfg.Providers["simpleswap"]; ok && ssCfg.APIKey != "" {
		ssProvider := simpleswap.NewProvider(ssCfg.APIKey, rpcClients)
		providers = append(providers, ssProvider)
		log.Println("SimpleSwap provider enabled")
	}

	// Initialize swap manager
	swapMgr := swaps.NewManager(providers...)

	// Initialize CoWSwap client for gas refills
	cowClient := cowswap.NewClient(rpcClients)
	log.Println("CoWSwap client enabled for gas refills")

	// Create and run bot
	b, err := bot.New(cfg, database, swapMgr, rpcClients, cowClient)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	// Start HTTP server
	srv := server.New(cfg, database, rpcClients)
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Start swap completion tracker
	ctx, cancel := context.WithCancel(context.Background())
	trk := tracker.New(cfg, database, swapMgr, cowClient, b.BotAPI())
	go trk.Run(ctx)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Println("Shutting down...")
		cancel()
		b.Stop()
		os.Exit(0)
	}()

	log.Println("Starting FundBot...")
	if err := b.Run(); err != nil {
		log.Fatalf("Bot error: %v", err)
	}
}
