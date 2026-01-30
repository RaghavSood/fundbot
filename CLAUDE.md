# CLAUDE.md

## Project Overview

Telegram bot for funding addresses with stablecoins on Avalanche and Base via Thorchain swaps. BIP39 mnemonic-based HD wallet derivation. Two modes: single (shared wallet) and multi (per-user + per-group wallets). Web dashboard with admin panel.

## Build & Run

```bash
go build ./...          # build all packages
go vet ./...            # lint
sqlc generate           # regenerate db/*.sql.go from db/queries/*.sql
goose -dir db/migrations sqlite3 fundbot.db up  # run migrations
```

## Key Conventions

- **SQL**: sqlc for type-safe queries (`db/queries/*.sql` → `db/*.sql.go`), goose for migrations (`db/migrations/`)
- **Commit often**: Make a git commit after completing each meaningful piece of work (feature, bugfix, refactor). Don't batch multiple unrelated changes into one commit.
- **Commit style**: Prefix with `feat:`, `fix:`, `refactor:`, etc. Keep messages concise.
- **Config**: TOML config file. `mode = "single"` or `mode = "multi"`.
- **Wallet derivation**: BIP44 m/44'/60'/0'/0/{index}. Single mode: index 0. Multi mode DM: user DB row ID. Multi mode group: chat DB row ID.
- **Bot auth**: Single mode rejects groups. Multi mode groups allow all users. DMs check whitelist.
- **Tracker notifications**: Send to `chat_id` from topup record (falls back to `user_id` for legacy).
