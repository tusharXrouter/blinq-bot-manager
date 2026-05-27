# Bet Bot

A Go-based automated betting bot for prediction markets on Monad.

## Features

- **Hedged Betting**: Place opposite bets (UP/DOWN) from two different wallets - one always wins
- **Candle Rush**: Bet on candle color (GREEN/RED) across multiple assets in one transaction
- **Deterministic Wallets**: Generate up to 1250 wallets from a single mnemonic for easy recovery
- **Referral System**: Automatically redeem referral codes and create unique usernames for each wallet
- **Multi-wallet System**: Simulates organic activity with random wallet selection
- **Multi-threaded**: Run multiple concurrent betting workers
- **Both Market Types**: Supports UP/DOWN and RELATIVE predictions
- **Auto-funding**: Automatically funds wallets with USDC and MON from owner wallet
- **Encrypted Keystore**: AES-256-GCM encryption for wallet private keys at rest
- **PostgreSQL Storage**: Wallet data stored in PostgreSQL for production reliability and concurrent access

## Quick Start

The repo ships **three binaries**, all with interactive prompts:

| Binary | Purpose |
|---|---|
| `bet-bot-manager` | Orchestrator. Prompts which bot(s) to run (price arena, candle rush, or both) and then runs them with a periodic sweep job. |
| `test-bet-bot-manager` | No-tx flow tester. Prompts which bot(s) to validate and exercises Hasura + Hermes + strategy paths end-to-end. No keys, passphrase, or DB needed. |
| `sweep-manager` | Interactive sweeper. Prompts whether to sweep USDC, MON, or both from sub-wallets back to the owner wallet. |

```bash
# 1. Install dependencies
go mod tidy

# 2. Build all three binaries
go build -o bet-bot-manager ./cmd/bet-bot-manager
go build -o test-bet-bot-manager ./cmd/test-bet-bot-manager
go build -o sweep-manager ./cmd/sweep-manager

# 3. Configure environment
cp .env.example .env
# Edit .env with your values (DATABASE_URL, OWNER_PRIVATE_KEY, etc.)

# 4. Validate the flow (no transactions, no keys)
./test-bet-bot-manager

# 5. Run the orchestrator
./bet-bot-manager
```

See [TEST.md](./TEST.md) for the full testing playbook.

## Prerequisites

- **Go 1.24+**
- **PostgreSQL** — wallet data is stored in a `market_maker_wallets` table
- Set `DATABASE_URL` in your `.env` file:
  ```
  DATABASE_URL=postgres://user:password@host:port/dbname?sslmode=require
  ```
  The bot auto-creates the table on first run.

## Commands

### Bet-Bot Manager (`./bet-bot-manager`)

Orchestrator that runs the price-arena bot, the candle-rush bot, and a periodic sweep. On startup it asks which bots to run; choose your preset and the manager handles the rest.

```bash
# Interactive (prompts which bots to run)
./bet-bot-manager

# Non-interactive (uses config.yaml manager.enabled_bots verbatim)
./bet-bot-manager --non-interactive

# Generate N deterministic wallets from mnemonic before running
./bet-bot-manager --generate 100
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--non-interactive` | `false` | Skip the bot-selection prompt (use `manager.enabled_bots`) |
| `--dry-run` | `false` | Run both bots in simulation mode (no on-chain tx) |
| `--generate` | `0` | Pre-generate N wallets from the mnemonic before betting |

Configured via `config.yaml` under the `manager:` section:
```yaml
manager:
  enabled_bots: ["bet-bot", "candle-rush-bot"]
  sweep_interval_hours: 8
```

### Test Bet-Bot Manager (`./test-bet-bot-manager`)

Exercises the full read-side flow of both bots **without sending any transactions**. Asks which bot(s) you want to test, then runs:

- Loads `config.yaml`
- Hits Hasura (`HASURA_URL`) for live markets and reports coverage for every enabled asset
- Runs the strategy selection logic against those markets
- Hits Hermes (`HERMES_URL`) for live prices on the selected bet's price IDs
- For candle-rush: validates intervals, assets, halt-time math, and round-size sanity

No keystore passphrase, owner key, or `DATABASE_URL` is required.

```bash
# Interactive
./test-bet-bot-manager

# Test only the price-arena flow with 5 selection cycles
./test-bet-bot-manager --target price-arena --cycles 5 --non-interactive

# Test only the candle-rush flow
./test-bet-bot-manager --target candle-rush --non-interactive
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--target` | (prompt) | `both`, `price-arena`, or `candle-rush` |
| `--cycles` | `1` | Selection cycles per bot |
| `--non-interactive` | `false` | Skip the target prompt; `--target` becomes required |

See [TEST.md](./TEST.md) for the full testing playbook.

### Sweep Manager (`./sweep-manager`)

Forwards USDC and/or MON from every sub-wallet in the wallet store back to the owner wallet. On startup it asks which token(s) to sweep.

```bash
# Interactive
./sweep-manager

# Sweep both USDC and MON, no prompt (good for cron)
./sweep-manager --tokens both --non-interactive

# Sweep only USDC
./sweep-manager --tokens usdc --non-interactive

# Sweep only MON
./sweep-manager --tokens mon --non-interactive
```

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.yaml` | Path to config file |
| `--tokens` | (prompt) | `both`, `usdc`, or `mon` |
| `--non-interactive` | `false` | Skip the token prompt; `--tokens` becomes required |

The sweeper requires `KEYSTORE_PASSPHRASE` and `OWNER_PRIVATE_KEY` (loaded the same way as the orchestrator). For USDC sweeps it tops a sub-wallet up with the gas it needs from the owner wallet before transferring.

## How It Works

### 1. Wallet Creation (Deterministic)

Wallets are generated from a mnemonic using `Keccak256(mnemonic:index)`:

```
Index 0 -> Wallet A (0x123...)
Index 1 -> Wallet B (0x456...)
Index 2 -> Wallet C (0x789...)
...
```

- Up to **1250 wallets** can be generated
- All wallets recoverable with just the mnemonic
- Wallets stored in PostgreSQL (`market_maker_wallets` table)

### 2. Referral Setup (Per Wallet)

Referral is handled automatically whenever a wallet is used for betting:
1. Authenticate with API (sign challenge)
2. Redeem configured referral code (if `REFERRAL_CODE` is set and not yet redeemed)
3. Create unique referral code (e.g., `NEON-TIGER-42`) if not yet created
4. Store referral status in database

Referral is idempotent - already-redeemed/created codes are safely skipped. Wallets generated via `--generate` get their referral redeemed on first bet use.

### 3. Hedged Betting Strategy

```
Wallet 1: BTC UP   ($0.50)
Wallet 2: BTC DOWN ($0.50)
         |
One bet always wins (minus fees)
```

- Two different wallets place opposite bets
- Works with UP/DOWN markets
- For RELATIVE: HIGHEST vs LOWEST

### 4. Execution Flow

```
1. Fetch markets from Hasura
2. Select random bet
3. Get two distinct wallets
4. Fund wallets (USDC + MON)
5. Fetch fresh prices from Pyth
6. Execute primary bet (UP)
7. Execute opposite bet (DOWN)
8. Wait for cooldown
9. Repeat
```

## Database Schema

Wallet data is stored in a PostgreSQL `market_maker_wallets` table:

```sql
CREATE TABLE market_maker_wallets (
    address           TEXT PRIMARY KEY,
    private_key       TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    bet_count         INTEGER NOT NULL DEFAULT 0,
    total_volume_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_balance_usd  DOUBLE PRECISION NOT NULL DEFAULT 0,
    referral_redeemed BOOLEAN NOT NULL DEFAULT FALSE,
    my_referral_code  TEXT NOT NULL DEFAULT ''
);
```

| Column | Description |
|--------|-------------|
| `address` | Ethereum wallet address (primary key) |
| `private_key` | Encrypted private key (`enc:` prefix when keystore enabled) |
| `created_at` | When the wallet was generated |
| `last_used` | Last time the wallet placed a bet |
| `bet_count` | Total number of bets placed |
| `total_volume_usd` | Cumulative bet volume in USDC |
| `last_balance_usd` | Last known USDC balance of the wallet |
| `referral_redeemed` | Whether a referral code has been redeemed |
| `my_referral_code` | The referral code this wallet created |

The table is auto-created on first connection. To migrate from the legacy `wallets.json` format, use the `migrate-wallets` command.

## Configuration

### Secrets (runtime prompts)

Sensitive values can live in `.env` (loaded by godotenv at startup) or in Docker secrets — the bot only prompts when *neither* is set. Resolution order, applied to every secret independently:

1. **Docker secret** — `/run/secrets/<name>` (best for containers)
2. **Environment variable** — e.g. `OWNER_PRIVATE_KEY` (loaded from `.env`, works everywhere)
3. **Interactive prompt** — hidden terminal input at startup (most secure for local)

Every bot prints the resolved source on startup so you can confirm at a glance that no prompt fired for a value you thought you had configured:

```
Keystore: Enabled (passphrase from env)
Owner key: loaded from env
```

If you'd prefer to *only* get prompted for the passphrase, set `OWNER_PRIVATE_KEY` in `.env` (and `MNEMONIC` too if you use `--generate`).

| Secret | Env Var | Docker Secret Name | When Needed |
|--------|---------|-------------------|-------------|
| Owner private key | `OWNER_PRIVATE_KEY` | `owner_private_key` | Always (bet-bot, sweep) |
| Keystore passphrase | `KEYSTORE_PASSPHRASE` | `keystore_passphrase` | Always (bet-bot, sweep) |
| Mnemonic | `MNEMONIC` | `mnemonic` | Only with `--generate` |
| Database URL | `DATABASE_URL` | — | Always |

### Environment Variables (`.env`)

```bash
# Database (required)
DATABASE_URL=postgres://user:password@host:5432/dbname?sslmode=require

# Non-secret config (safe to keep in .env)
HASURA_URL=https://your-hasura-endpoint/v1/graphql
INITIAL_WALLET_COUNT=100
REFERRAL_CODE=YOUR-REFERRAL-CODE
API_BASE_URL=https://prediction-api.blinq.fi
SINGLE_WALLET_MODE=false
GENERATE_NEW_WALLETS=true
COOLDOWN_SECONDS=30
THREADS=1
```

### Config File (`config.yaml`)

```yaml
# Network
rpc_url: 'https://rpc.monad.xyz'
chain_id: 143

# Contracts
diamond_address: '0x928dc8afe312df45576b15b08c086c5427fd8207'
usdc_address: '0x754704Bc059F8C67012fEd69BC8A327a5aafb603'

# Wallet Management
wallets:
  db_url: '${DATABASE_URL}'
  new_wallet_probability: 0.0
  min_fund_amount_usdc: 1.0
  max_fund_amount_usdc: 3.0
  min_gas_fund_mon: 0.5
  max_gas_fund_mon: 1.0
  min_gas_balance_mon: 0.05
  min_wallets_before_reuse: 5
  generate_new_wallets: true  # false = only use existing wallets

# Betting
betting:
  min_amount_usdc: 0.1
  max_amount_usdc: 1.0
  cooldown_seconds: 30
  tolerance_bps: 500          # 5% price tolerance
  max_price_age_seconds: 10
  price_retries: 3
  broker_id: 1
  enabled_assets:
    - 'BTC'
    - 'ETH'
    - 'SOL'
  enabled_timeframes:
    - '5m'
    - '15m'
    - '30m'

# Assets
assets:
  BTC:
    address: '0x0555E30da8f98308EdB960aa94C0Db47230d2B9c'
    price_id: '0xe62df6c8b4a85fe1a67db44dc12de5db330f7ac66b72dc658afedf0f4a415b43'
  ETH:
    address: '0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242'
    price_id: '0xff61491a931112ddf1bd8147cd1b641375f79f5825126d665480874634fd0ace'
  SOL:
    address: '0xea17E5a9efEBf1477dB45082d67010E2245217f1'
    price_id: '0xef0d8b6fda2ceba41da15d4095d1da392a0d2f8ed0c6c7bc0f4cfac8c280b56d'
```

## Project Structure

```
bet-bot/
├── cmd/
│   ├── bet-bot-manager/main.go       # Interactive orchestrator (price arena + candle rush + sweep)
│   ├── test-bet-bot-manager/main.go  # No-tx flow tester (asks which bot)
│   └── sweep-manager/main.go         # Interactive sweep (USDC/MON/both)
├── internal/
│   ├── betting/              # Strategy & executor for price arena
│   ├── bot/                  # Betting cycle engine (price arena)
│   ├── candlerush/           # Candle rush executor
│   ├── cli/                  # CLI formatting + interactive prompts
│   ├── config/               # Configuration loading
│   ├── contracts/            # Diamond, ERC20 & CandleRush bindings
│   ├── markets/              # Hasura market fetcher
│   ├── notify/               # Slack low-balance alerts
│   ├── prices/               # Pyth/Hermes price fetcher
│   ├── referral/             # Referral API client
│   ├── secret/               # Docker secret / env / interactive prompt loader
│   ├── utils/                # Name generation
│   └── wallet/               # Wallet management & encrypted keystore
├── config.yaml               # Bot configuration
├── .env                      # Environment variables (see .env.example)
├── TEST.md                   # Testing playbook
├── Dockerfile
└── docker-compose.yml
```

## Docker

The `bet-bot-manager` service runs as a daemon. `sweep-manager` and `test-bet-bot-manager` are exposed under the `tools` profile and are run on demand.

```bash
# Build and run the orchestrator
docker compose up --build -d

# One-shot interactive sweep
docker compose run --rm sweep-manager

# Validate flow against live endpoints (no transactions)
docker compose run --rm test-bet-bot-manager
```

## Contract Addresses (Monad)

| Contract | Address |
|----------|---------|
| Diamond | `0x928dc8afe312df45576b15b08c086c5427fd8207` |
| USDC | `0x754704Bc059F8C67012fEd69BC8A327a5aafb603` |
| BTC | `0x0555E30da8f98308EdB960aa94C0Db47230d2B9c` |
| ETH | `0xEE8c0E9f1BFFb4Eb878d8f15f368A02a35481242` |
| SOL | `0xea17E5a9efEBf1477dB45082d67010E2245217f1` |

## Encrypted Keystore

All wallet private keys in the database are encrypted at rest using AES-256-GCM with Argon2id key derivation. The `KEYSTORE_PASSPHRASE` is **required** — the bot will refuse to start without it.

### Providing the Passphrase

The passphrase is resolved in this order (first match wins):

1. **Docker secret** — file at `/run/secrets/keystore_passphrase`
2. **Environment variable** — `KEYSTORE_PASSPHRASE`
3. **Interactive prompt** — secure hidden input at startup

```
$ ./bot --hedged
  Enter keystore passphrase: ********
  Keystore: Enabled (keys encrypted at rest)
```

For Docker, use Docker secrets (mounted as tmpfs, invisible to `docker inspect`).

On first startup, all existing plaintext keys are automatically encrypted (one-time migration). The encrypted format uses the `enc:` prefix:

```
enc:base64(salt + nonce + ciphertext)
```

### How It Works

- **Key derivation**: Argon2id (time=3, memory=64MB, threads=4) derives a 256-bit key from your passphrase
- **Encryption**: AES-256-GCM with a unique random salt (16 bytes) and nonce (12 bytes) per key
- **Migration**: Automatic on first startup with a passphrase — encrypts all plaintext keys in the database
- **Runtime**: Keys are decrypted on-the-fly when accessed for betting or sweeping (~50-100ms per key)

### Important Notes

- **Do not lose your passphrase** — encrypted keys cannot be recovered without it
- Starting without `KEYSTORE_PASSPHRASE` will exit with an error
- Starting with a wrong passphrase will exit with a "decryption failed" error

## Security Notes

- Keep `.env` file secure - never commit to version control
- Private keys in the database are encrypted with `KEYSTORE_PASSPHRASE`
- Use a dedicated owner wallet with limited funds
- The mnemonic can recover all wallets - store it safely
- `DATABASE_URL` contains credentials - treat it as a secret

## License

MIT
