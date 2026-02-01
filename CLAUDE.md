# CLAUDE.md

## Project Overview

FundBot (GiveWei) — Telegram bot for funding crypto addresses via swap providers (Thorchain, SimpleSwap, Near Intents, Houdini Swap). Sources USDC from Avalanche and Base EVM chains, swaps to 29+ target assets. BIP39 mnemonic-based HD wallet derivation. Two modes: single (shared wallet) and multi (per-user + per-group wallets). Web dashboard with admin panel using Tailwind CSS v4.

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
- `Execute()` returns `ExecuteResult{TxHash, ExternalID}` — ExternalID is for provider-specific tracking (e.g. SimpleSwap exchange ID, Houdini houdiniId)
- `CheckStatus()` accepts `externalID` param — Thorchain ignores it, SimpleSwap/Houdini use it to poll exchange status
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

### Houdini Swap Provider (`houdini/`)
- Custodial exchange model (CEX routes only): create exchange via API → get deposit address → plain ERC20 transfer of USDC
- Status tracking via Houdini houdiniId (stored in `topups.external_id` column)
- Static asset mapping in `houdini/mapping.go` — maps Thorchain-notation assets (e.g. `BTC.BTC`) to Houdini token IDs (e.g. `BTC`)
- Authentication: `Authorization: ApiKey:ApiSecret` header
- API base URL: `https://api-partner.houdiniswap.com`
- Numeric status codes: 0=waiting, 1=confirming, 2=exchanging, 3=sending, 4=completed, 5+=failed
- Config: `"providers": {"houdini": {"api_key": "...", "api_secret": "..."}}` — nested under `providers` key
- Source USDC symbols: `USDCAVAXC` (Avalanche), `USDCBASE` (Base)
- Dynamic minimums via `/getMinMax` API (non-anonymous ~$10, anonymous ~$50)
- **Anonymous routing** (`houdini-anon` provider, `hanon` hint): anonymous swaps via `anonymous=true`. Quote IDs are intentionally omitted on `/exchange` (Houdini API bug: quote IDs + anonymous=true → 500). The API re-quotes internally. Category `"anon-private"` — excluded from normal routing, only activated explicitly.

### CoWSwap (`cowswap/`)
- Client for CoW Protocol API — currently used for gas refills, designed for future general swap support
- Supports Base and Avalanche chains (`api.cow.fi/base`, `api.cow.fi/avalanche`)
- Core methods: `GetQuote()`, `SignOrder()` (EIP-712), `SubmitOrder()`, `CheckOrderStatus()` — all public for reuse
- `RegisterAppData()` uploads appData JSON to CoW API via `PUT /app_data/{hash}` (kept for general use, not needed for order submission which accepts inline full JSON)
- Gasless approval via EIP-2612 permit: signs permit off-chain, embeds as CoW pre-hook in appData
- If vault relayer allowance sufficient, uses default appData (no hooks); otherwise builds permit pre-hook
- `RefillGasIfNeeded()`: high-level gas refill — checks threshold, quotes, signs, submits
- Settlement contract: `0x9008D19f58AAbD9eD0D60971565AA8510560ab41` (same on all chains)
- Vault Relayer: `0xC92E8bdf79f0507f65a392b0ab4667716BFE0110` (spender for approvals/permits)
- Native token buy address: `0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE`
- Gas refill triggered by `/balance` command when native balance < ~$1 worth and USDC balance >= $5
- Test script: `cmd/cowtest/main.go` — standalone USDC→AVAX swap on Avalanche with permit, useful for debugging

#### CoW Protocol API Gotchas
- **USDC permit domain name**: The on-chain `name()` returns `"USD Coin"`, NOT `"USDC"`. Using the wrong name causes `ecrecover` to recover the wrong address → `"EIP2612: invalid signature"`. Always verify domain params by calling `name()` and `version()` on the token contract.
- **feeAmount must be "0"**: Both order signing (EIP-712) and submission must use `feeAmount="0"`. Solvers compute fees dynamically. Forwarding the non-zero fee from the quote response causes `"NonZeroFee"` rejection.
- **appData in order submission**: The `appData` field accepts either the full JSON string or a bytes32 hash. When submitting with permit pre-hooks, send the **full JSON** so the backend auto-registers it and can simulate the hook during validation. The quote API response returns `appData` as the full JSON and `appDataHash` as the bytes32 — use `appDataHash` for EIP-712 signing (it's the `bytes32` type in the Order struct).
- **Pre-hook validation**: CoW backend simulates pre-hooks via `eth_call` before accepting orders. If the permit signature is invalid, the simulation reverts, allowance stays 0, and the API returns `InsufficientAllowance` — the error is misleading since the real issue is the permit signature.
- **quoteId**: Include `quoteId` from the quote response in the order submission for better matching.

#### EIP-712 Signing Details
- **Order signing domain**: `{name: "Gnosis Protocol", version: "v2", chainId, verifyingContract: settlement}`
- **USDC permit domain**: `{name: "USD Coin", version: "2", chainId, verifyingContract: USDC address}` — both Avalanche and Base use the same name/version
- **Permit value**: Use max `uint256` so the permit doesn't need to be repeated for subsequent swaps

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
