# Testing Playbook

Every flow in this repo can be exercised end-to-end **without sending a single transaction** via `test-bet-bot-manager`. This document is the single source of truth for how to validate the bot before a deploy.

## Quick reference

| Command | Validates |
|---|---|
| `./test-bet-bot-manager` | Interactive — asks which flow to validate |
| `./test-bet-bot-manager --target both --non-interactive` | Both flows end-to-end |
| `./test-bet-bot-manager --target price-arena --non-interactive` | Price-arena (bet-bot) flow only |
| `./test-bet-bot-manager --target candle-rush --non-interactive` | Candle-rush flow only |
| `./test-bet-bot-manager --cycles 5 --non-interactive` | Multiple selection cycles per bot |

The runner needs nothing more than `config.yaml` and a populated `HASURA_URL` and `HERMES_URL` in `.env`. **No keystore passphrase, owner key, mnemonic, or DATABASE_URL is required.**

## What gets tested

### Price-arena (bet-bot)

1. **Config load** — `config.yaml` parses; `betting.enabled_assets` and `betting.enabled_timeframes` are non-empty.
2. **Hasura fetch** — calls `HASURA_URL` for both `UP_DOWN` and `RELATIVE` markets. Logs counts and elapsed time.
3. **Market coverage** — every asset in `betting.enabled_assets` is checked against the Hasura response. Surfaces the same "missing from Hasura" diagnostic that surfaced the FE-vs-bot endpoint drift bug.
4. **Strategy selection** — calls `betting.Strategy.SelectRandomBet` against the real market data. Honours `trading_hours` so equities/commodities are skipped outside their schedule.
5. **Price freshness** — for the asset(s) the strategy picked, fetches Hermes prices and prints `price/expo/age`. A cycle is only counted as passed if Hermes returned fresh data (< 30 s old, with 2 retries).

### Candle-rush

1. **Config load** — `candle_rush.intervals` and `candle_rush.assets` non-empty.
2. **Coverage math** — verifies `target_covered < visible_candles` and computes the halt duration per interval.
3. **Round-size sanity** — prints worst-case USDC needed per round.
4. **Price feeds** — Hermes is hit for every configured candle-rush asset; prices must be fresh.

The runner does **not** call any on-chain method, so no RPC/Diamond/USDC contract is touched. Wallet generation is also skipped.

## How to run

### Locally

```bash
go build -o test-bet-bot-manager ./cmd/test-bet-bot-manager

# Interactive
./test-bet-bot-manager

# CI / scripted
./test-bet-bot-manager --target both --cycles 3 --non-interactive
```

### Docker

```bash
docker compose run --rm test-bet-bot-manager --target both --cycles 3 --non-interactive
```

## Reading the output

A healthy run looks like this (excerpted):

```
═══ TEST BET-BOT MANAGER ═══

  Hasura:    https://beta.api.blinq.fi/graph/v1/graphql
  Cycles:    1 per bot
  Target:    price-arena + candle-rush

── PRICE ARENA (bet-bot) ──
  ✓ fetched markets in 375ms: up_down=13 relative=16
  market coverage:
    BTC present
    ETH present
    ...
  Cycle 1:
    ⏸  skipping AAPL markets: market closed (...)
    ✓ UP_DOWN bet — ETH/USD DOWN @ 5m (0.3739 USDC)
      id=ff61491a93… price=211699337772 expo=-8 age=2s
  ✓ price-arena: 1/1 cycles produced a complete bet

── CANDLE RUSH (candle-rush-bot) ──
  ✓ assets:     BTC, ETH, SOL
  ✓ intervals:  [300 900 1800] seconds
  ✓ coverage:   8/14 (gap=0.75)
  ✓ worst-case round size: 90.00 USDC
  ...
═══ ALL TESTS PASSED ═══
```

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `fetch markets: context deadline exceeded` | `HASURA_URL` is unreachable or wrong | Update `.env` `HASURA_URL` (FE uses `https://beta.api.blinq.fi/graph/v1/graphql`) |
| `market coverage: <ASSET> missing from Hasura` | Hasura response doesn't contain an UP_DOWN market for the asset configured in `config.yaml` | Either drop the asset from `betting.enabled_assets` or wait for it to be re-listed |
| `select bet: no valid markets available` | Every market got filtered (closed trading hours / no enabled timeframe matches) | Run during open hours, or expand `betting.enabled_timeframes` |
| `fetch prices: ... stale` | Hermes gateway is slow or rate-limited | Set `HERMES_URL` to a paid Pyth gateway; default `hermes.pyth.network` rate-limits in CI |
| `target_covered must be < visible_candles` | `config.yaml` `candle_rush` block is misconfigured | Lower `target_covered` or raise `visible_candles` |
| 1e8 price looks 10⁵+ off from the actual USD value | A regression of the Pyth expo conversion bug (see `internal/prices/pyth_test.go`) | The runner now prints `raw=… expo=… → 1e8=… ($…)` for every leg. The dollar value in the parentheses must visually match the real market price; if not, `ConvertPythPriceTo1e8` has regressed. |

### Pyth expo sanity check

Crypto majors (BTC, ETH, SOL) ship at `expo=-8`, so their 1e8 representation equals the raw Pyth integer. Commodities (GOLD `-3`, SILVER/WTI `-5`) and US equities ship at coarser scales — the runner prints the converted 1e8 value and dollar equivalent for every leg of every selected bet. After a successful run, eyeball the `$…` column:

```
WTI vs GOLD vs SILVER HIGHEST @ 5m (0.4953 USDC)
  id=925ca92ff0… raw=9207145    expo=-5  → 1e8=9187482000     ($91.87)
  id=765d2ba906… raw=4526854    expo=-3  → 1e8=452685400000   ($4526.85)
  id=f2fb02c32b… raw=7635482    expo=-5  → 1e8=7635482000     ($76.35)
```

If any of those dollar figures is orders of magnitude wrong, the on-chain `acceptablePrice` will be too — the contract will either revert or fill at an absurd price. `internal/prices/pyth_test.go` is the unit-test guard for this.

## After a green test

Run the production orchestrator once the test passes:

```bash
./bet-bot-manager                   # interactive
./bet-bot-manager --non-interactive # for systemd/docker
```

Or sweep funds back to the owner before shutting down a host:

```bash
./sweep-manager                                    # interactive
./sweep-manager --tokens both --non-interactive    # for cron
```

## CI integration

Add a job that runs the test runner against staging endpoints with `--non-interactive`. Exit code is `0` on pass, `1` on fail.

```yaml
# .github/workflows/bet-bot-smoke.yml  (example)
- name: Smoke-test bet-bot
  env:
    HASURA_URL: https://beta.api.blinq.fi/graph/v1/graphql
    HERMES_URL: ${{ secrets.HERMES_URL }}
  run: |
    go build -o test-bet-bot-manager ./cmd/test-bet-bot-manager
    ./test-bet-bot-manager --target both --cycles 3 --non-interactive
```
