package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/blinq-fi/blinq-mm-bot/internal/betting"
	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
	"github.com/blinq-fi/blinq-mm-bot/internal/prices"
)

// Standalone flow test: fetches real Hasura markets, runs strategy selection,
// and (optionally) fetches Hermes prices for the picked bet. Does not need
// private keys, passphrases, DB, or RPC — and never sends a transaction.

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	hasuraOverride := flag.String("hasura-url", "", "Override HASURA_URL from env/config")
	hermesOverride := flag.String("hermes-url", "", "Override HERMES_URL from env/config")
	skipPrices := flag.Bool("skip-prices", false, "Skip Hermes price fetch")
	cycles := flag.Int("cycles", 1, "Number of selection cycles to run")
	flag.Parse()

	_ = godotenv.Load()

	cfg, err := loadConfigLoose(*configPath)
	if err != nil {
		fmt.Printf("✗ load config: %v\n", err)
		os.Exit(1)
	}

	if *hasuraOverride != "" {
		cfg.HasuraURL = *hasuraOverride
	}
	if *hermesOverride != "" {
		cfg.HermesURL = *hermesOverride
	}
	if cfg.HasuraURL == "" {
		fmt.Println("✗ HASURA_URL not set (use --hasura-url or set HASURA_URL env var)")
		os.Exit(1)
	}

	fmt.Println("=== bet-bot flow test (no transactions) ===")
	fmt.Printf("Hasura:     %s\n", cfg.HasuraURL)
	fmt.Printf("Hermes:     %s\n", maskOrShow(cfg.HermesURL))
	fmt.Printf("Assets cfg: %d entries\n", len(cfg.Assets))
	fmt.Printf("Enabled:    %v\n", cfg.Betting.EnabledAssets)
	fmt.Printf("Timeframes: %v\n", cfg.Betting.EnabledTimeframes)
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Fetch markets
	fetcher := markets.NewFetcher(cfg.HasuraURL, "")
	t0 := time.Now()
	upDown, relative, err := fetcher.FetchAllMarkets(ctx)
	if err != nil {
		fmt.Printf("✗ fetch markets: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ fetched markets in %s: up_down=%d relative=%d\n",
		time.Since(t0).Round(time.Millisecond),
		len(upDown.Markets), len(relative.Markets))

	// Show diagnostics: which configured/enabled assets actually have a market
	reportCoverage(cfg, upDown.Markets, relative.Markets)

	// 2. Build strategy and try selecting bets
	strat := betting.NewStrategy(cfg.Betting, cfg.Assets)

	successes := 0
	for i := 1; i <= *cycles; i++ {
		fmt.Printf("\n--- Cycle %d ---\n", i)
		bet, err := strat.SelectRandomBet(upDown.Markets, relative.Markets)
		if err != nil {
			fmt.Printf("✗ select bet: %v\n", err)
			continue
		}
		successes++
		printBet(bet)

		if *skipPrices || cfg.HermesURL == "" {
			continue
		}

		// 3. Fetch prices for the selected bet's price IDs
		pf := prices.NewFetcher(cfg.HermesURL)
		pctx, pcancel := context.WithTimeout(ctx, 15*time.Second)
		priceData, perr := pf.FetchFreshPrices(pctx, bet.PriceIDs, 30*time.Second, 2)
		pcancel()
		if perr != nil {
			fmt.Printf("  ✗ fetch prices: %v\n", perr)
		} else {
			fmt.Printf("  ✓ prices: %d ids fetched\n", len(priceData))
			for id, p := range priceData {
				fmt.Printf("      id=%s… price=%s expo=%d age=%ds\n",
					shortID(id), p.Price.Price, p.Price.Expo,
					time.Now().Unix()-p.Price.PublishTime)
			}
		}
	}

	fmt.Printf("\n=== done: %d/%d cycles selected a bet ===\n", successes, *cycles)
}

// loadConfigLoose mirrors config.Load but skips validation that would require
// secrets (owner_private_key, etc) so we can run with only HASURA_URL.
func loadConfigLoose(path string) (*config.Config, error) {
	// We can't reuse config.Load because it calls Validate() which requires
	// the owner private key. Build a tiny YAML reader inline by leveraging
	// viper through a copy of the public loader, but instead just re-use
	// config.LoadCandleRush which does relaxed validation.
	cfg, err := config.LoadCandleRush(path)
	if err != nil {
		return nil, err
	}
	// LoadCandleRush doesn't expand HASURA_URL; do it manually.
	if strings.HasPrefix(cfg.HasuraURL, "${") && strings.HasSuffix(cfg.HasuraURL, "}") {
		envVar := cfg.HasuraURL[2 : len(cfg.HasuraURL)-1]
		cfg.HasuraURL = os.Getenv(envVar)
	}
	if cfg.HasuraURL == "" {
		cfg.HasuraURL = os.Getenv("HASURA_URL")
	}
	if cfg.HermesURL == "" {
		cfg.HermesURL = os.Getenv("HERMES_URL")
	}
	return cfg, nil
}

func reportCoverage(cfg *config.Config, upDown, rel []markets.Market) {
	// Map every Hasura-returned UP_DOWN base address to a configured symbol
	addrToSymbol := map[string]string{}
	for sym, a := range cfg.Assets {
		addrToSymbol[strings.ToLower(a.Address)] = strings.ToUpper(sym)
	}

	apiSymbols := map[string]bool{}
	for _, m := range upDown {
		if m.Base == nil {
			continue
		}
		if sym, ok := addrToSymbol[strings.ToLower(*m.Base)]; ok {
			apiSymbols[sym] = true
		}
	}

	fmt.Println("\nmarket coverage for enabled assets:")
	var missing []string
	for _, sym := range cfg.Betting.EnabledAssets {
		up := strings.ToUpper(sym)
		if apiSymbols[up] {
			fmt.Printf("  %s: present\n", up)
		} else {
			fmt.Printf("  %s: not present in Hasura response\n", up)
			missing = append(missing, up)
		}
	}
	if len(missing) > 0 {
		fmt.Printf("  missing_from_api: %s\n", strings.Join(missing, ", "))
	}
}

func printBet(b *betting.BetSelection) {
	fmt.Printf("✓ selected %s bet\n", b.Type)
	fmt.Printf("  market:    %s\n", b.GetMarketDescription())
	fmt.Printf("  direction: %s\n", b.GetDirectionString())
	fmt.Printf("  period:    %s (id=%s, %ss)\n",
		markets.PeriodToTimeframe[b.Period.Period], b.Period.Period, b.Period.PeriodSeconds)
	fmt.Printf("  amount:    %.4f USDC\n", b.Amount)
	fmt.Printf("  asset:     %s\n", b.AssetSymbol)
	fmt.Printf("  price_ids: %v\n", b.PriceIDs)
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func maskOrShow(u string) string {
	if u == "" {
		return "(not set)"
	}
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i] + "?***"
	}
	return u
}
