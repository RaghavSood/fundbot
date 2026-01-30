# CLAUDE.md

## Project Overview

FundBot (GiveWei) — Telegram bot for funding crypto addresses via swap providers (Thorchain, SimpleSwap). Sources USDC from Avalanche and Base EVM chains, swaps to 29+ target assets. BIP39 mnemonic-based HD wallet derivation. Two modes: single (shared wallet) and multi (per-user + per-group wallets). Web dashboard with admin panel using Tailwind CSS v4.

## Build & Run

```bash
go build ./...          # build all packages
go vet ./...            # lint
sqlc generate           # regenerate db/*.sql.go from db/queries/*.sql
goose -dir db/migrations sqlite3 fundbot.db up  # run migrations
```

Config is JSON (`config.json`). See `config.example.json` for structure.

## Key Conventions

- **SQL**: sqlc for type-safe queries (`db/queries/*.sql` → `db/*.sql.go`), goose for migrations (`db/migrations/`)
- **Commit often**: Make a git commit and push after completing each meaningful piece of work. Don't batch unrelated changes.
- **Commit style**: Prefix with `feat:`, `fix:`, `refactor:`, etc. Keep messages concise.
- **Config**: JSON config file. `mode: "single"` or `mode: "multi"`.
- **Frontend**: Tailwind CSS v4 via browser CDN (`@tailwindcss/browser@4`). No build step for CSS.

## Architecture

### Wallet Derivation
- BIP44 path: `m/44'/60'/0'/0/{index}`
- Single mode: index 0 (shared wallet)
- Multi mode: index from `address_assignments` table (unified autoincrement sequence for users and chats)
- The `address_assignments` table prevents index collisions between users and chats (both had autoincrement IDs starting from 1)

### Swap Providers
- **Provider interface** (`swaps/provider.go`): `Quote()`, `Execute()`, `CheckStatus()`
- `Execute()` returns `ExecuteResult{TxHash, ExternalID}` — ExternalID is for provider-specific tracking (e.g. SimpleSwap exchange ID)
- `CheckStatus()` accepts `externalID` param — Thorchain ignores it, SimpleSwap uses it to poll exchange status
- `Quote()` accepts `sender` address to check USDC balance per-chain before quoting — only chains with sufficient balance produce quotes
- **Manager** (`swaps/manager.go`): queries all providers, returns best quote by `ExpectedOutputRaw`

### Thorchain Provider (`thorchain/`)
- Router contract model: approve USDC → call `depositWithExpiry` on router
- Status tracking via Thorchain tx status API (outbound_signed or swap_finalised stages)
- Source assets defined in `thorchain/constants.go` (`SourceAssets`, `USDCContracts`)

### SimpleSwap Provider (`simpleswap/`)
- Custodial exchange model: create exchange via API → get deposit address → plain ERC20 transfer of USDC
- Status tracking via SimpleSwap exchange ID (stored in `topups.external_id` column)
- Static asset mapping in `simpleswap/mapping.go` — maps Thorchain-notation assets (e.g. `BTC.BTC`) to SimpleSwap symbols (e.g. `btc`)
- Supports 29+ target assets including BTC, ETH, SOL, AVAX, DOT, ADA, ATOM, XRP, etc.
- **Not available**: kuji (Kujira). **Note**: cro maps to ERC20 on ETH, not native Cronos.
- Config: `"providers": {"simpleswap": {"api_key": "..."}}` — nested under `providers` key
- Source USDC symbols: `usdcavaxc` (Avalanche), `usdcbase` (Base)

### Balance Checking
- `balances/` package provides `USDCBalance()` and `FetchBalances()` helpers
- `balances` package does NOT import `thorchain` (avoids import cycle) — USDC contract addresses are passed as parameters
- Both providers check wallet USDC balance before quoting to ensure correct chain selection

### Bot
- Commands: `/start`, `/help`, `/address`, `/balance` (alias `/balances`), `/quote`, `/topup`, `/status`, `/version`
- Auth: Single mode rejects groups. Multi mode groups allow all users. DMs check whitelist.
- Telegram Markdown: `reply()` falls back to plain text if Markdown parsing fails (handles special chars in error messages)
- Tracker notifications: Send to `chat_id` from topup record (falls back to `user_id` for legacy)

### Database Schema
- `users`: telegram users (autoincrement ID, telegram_id, username)
- `chats`: telegram group chats (autoincrement ID, chat_id, title)
- `address_assignments`: unified wallet index sequence (assigned_to_id, assigned_to_type of 'user'|'chat')
- `quotes`: stored quotes with provider, amounts, memo, router, vault
- `topups`: swap executions with `external_id` for provider-specific tracking, `short_id` for user-facing IDs
