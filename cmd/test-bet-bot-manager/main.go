// test-bet-bot-manager exercises the read-side flow of both bots without
// sending any blockchain transactions. It asks the user which bot to test
// (price arena, candle rush, or both) and validates each step end-to-end
// using real Hasura + Hermes endpoints. No keystore passphrase, owner key,
// or DB is required.
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
	"github.com/blinq-fi/blinq-mm-bot/internal/cli"
	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
	"github.com/blinq-fi/blinq-mm-bot/internal/prices"
)

const (
	choiceBoth        = 0
	choicePriceArena  = 1
	choiceCandleRush  = 2
)

func main() {
	_ = godotenv.Load()

	configPath := flag.String("config", "config.yaml", "Path to config file")
	target := flag.String("target", "", "Which bot(s) to test: both|price-arena|candle-rush (empty = prompt)")
	cycles := flag.Int("cycles", 1, "Selection cycles per bot")
	nonInteractive := flag.Bool("non-interactive", false, "Skip prompts; require --target")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Printf("  %s Load config: %v\n", cli.Error("✗"), err)
		os.Exit(1)
	}
	expandUrls(cfg)

	choice := resolveTarget(*target, *nonInteractive)

	fmt.Println(cli.Banner("TEST BET-BOT MANAGER"))
	fmt.Printf("  Hasura:    %s\n", cfg.HasuraURL)
	fmt.Printf("  Hermes:    %s\n", maskQuery(cfg.HermesURL))
	fmt.Printf("  Cycles:    %d per bot\n", *cycles)
	fmt.Printf("  Target:    %s\n", targetLabel(choice))
	fmt.Println()
	fmt.Println(cli.DimText("  No blockchain transactions are sent. No keys required."))
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	overall := true
	if choice == choiceBoth || choice == choicePriceArena {
		ok := testPriceArena(ctx, cfg, *cycles)
		overall = overall && ok
	}
	if choice == choiceBoth || choice == choiceCandleRush {
		ok := testCandleRush(ctx, cfg, *cycles)
		overall = overall && ok
	}

	fmt.Println()
	if overall {
		fmt.Println(cli.Banner("ALL TESTS PASSED"))
	} else {
		fmt.Println(cli.Banner("TESTS FAILED"))
		os.Exit(1)
	}
}

// testPriceArena runs the read-side flow for the bet-bot: fetch markets,
// filter by trading hours/asset config, pick a bet, fetch Hermes prices.
func testPriceArena(ctx context.Context, cfg *config.Config, cycles int) bool {
	fmt.Println(cli.BoldText(cli.CyanText("── PRICE ARENA (bet-bot) ──")))

	if cfg.HasuraURL == "" {
		fmt.Printf("  %s HASURA_URL is empty\n", cli.Error("✗"))
		return false
	}
	if len(cfg.Betting.EnabledAssets) == 0 {
		fmt.Printf("  %s betting.enabled_assets is empty in config.yaml\n", cli.Error("✗"))
		return false
	}

	// 1. Fetch markets
	t0 := time.Now()
	fetcher := markets.NewFetcher(cfg.HasuraURL, "")
	upDown, relative, err := fetcher.FetchAllMarkets(ctx)
	if err != nil {
		fmt.Printf("  %s fetch markets: %v\n", cli.Error("✗"), err)
		return false
	}
	fmt.Printf("  %s fetched markets in %s: up_down=%d relative=%d\n",
		cli.Success("✓"),
		time.Since(t0).Round(time.Millisecond),
		len(upDown.Markets), len(relative.Markets))

	// 2. Coverage report
	reportCoverage(cfg, upDown.Markets)

	// 3. Strategy selection over N cycles
	strat := betting.NewStrategy(cfg.Betting, cfg.Assets)
	picks := 0
	for i := 1; i <= cycles; i++ {
		fmt.Printf("\n  Cycle %d:\n", i)
		bet, err := strat.SelectRandomBet(upDown.Markets, relative.Markets)
		if err != nil {
			fmt.Printf("    %s select bet: %v\n", cli.Error("✗"), err)
			continue
		}
		picks++
		fmt.Printf("    %s %s bet — %s %s @ %s (%.4f USDC)\n",
			cli.Success("✓"), bet.Type,
			bet.GetMarketDescription(),
			bet.GetDirectionString(),
			markets.PeriodToTimeframe[bet.Period.Period],
			bet.Amount)

		if cfg.HermesURL == "" {
			fmt.Printf("    %s HERMES_URL not set — skipping price fetch\n", cli.Warning("!"))
			continue
		}
		pf := prices.NewFetcher(cfg.HermesURL)
		priceMap, perr := pf.FetchFreshPrices(ctx, bet.PriceIDs, 30*time.Second, 2)
		if perr != nil {
			fmt.Printf("    %s fetch prices: %v\n", cli.Error("✗"), perr)
			picks--
			continue
		}
		for id, p := range priceMap {
			converted, cerr := prices.ConvertPythPriceTo1e8(p.Price.Price, p.Price.Expo)
			if cerr != nil {
				fmt.Printf("      id=%s… raw=%s expo=%d age=%ds  %s convert: %v\n",
					short(id), p.Price.Price, p.Price.Expo,
					time.Now().Unix()-p.Price.PublishTime,
					cli.Error("✗"), cerr)
				picks--
				continue
			}
			fmt.Printf("      id=%s… raw=%s expo=%d → 1e8=%d ($%.4f) age=%ds\n",
				short(id), p.Price.Price, p.Price.Expo,
				converted, float64(converted)/1e8,
				time.Now().Unix()-p.Price.PublishTime)
		}
	}

	fmt.Printf("\n  %s price-arena: %d/%d cycles produced a complete bet\n",
		successDot(picks == cycles), picks, cycles)
	return picks == cycles
}

// testCandleRush exercises the candle-rush bot's config + price-feed path.
// It does not need contracts — just validates the config and pulls Hermes
// prices for every asset that would be traded.
func testCandleRush(ctx context.Context, cfg *config.Config, cycles int) bool {
	fmt.Println()
	fmt.Println(cli.BoldText(cli.CyanText("── CANDLE RUSH (candle-rush-bot) ──")))

	cr := cfg.CandleRush

	// Resolve assets — uppercase symbols from candle_rush.assets
	assetNames := cr.Assets
	if len(assetNames) == 0 {
		assetNames = []string{"BTC", "ETH", "SOL"}
	}
	intervals := cr.Intervals
	if len(intervals) == 0 {
		fmt.Printf("  %s candle_rush.intervals is empty in config.yaml\n", cli.Error("✗"))
		return false
	}
	candlesPer := cr.CandlesPerInterval
	if candlesPer < 1 {
		candlesPer = 5
	}
	minAmt := cr.MinAmountUSDC
	maxAmt := cr.MaxAmountUSDC
	if minAmt <= 0 {
		minAmt = 2.0
	}
	if maxAmt <= 0 {
		maxAmt = 10.0
	}

	visible := cr.VisibleCandles
	if visible <= 0 {
		visible = 14
	}
	target := cr.TargetCovered
	if target <= 0 {
		target = 8
	}
	if target >= visible {
		fmt.Printf("  %s target_covered (%d) must be < visible_candles (%d)\n",
			cli.Error("✗"), target, visible)
		return false
	}
	gap := float64(visible-target) / float64(target)

	// Configuration summary
	fmt.Printf("  %s assets:     %s\n", cli.Success("✓"), strings.Join(assetNames, ", "))
	fmt.Printf("  %s intervals:  %v seconds\n", cli.Success("✓"), intervals)
	fmt.Printf("  %s candles:    %d per interval\n", cli.Success("✓"), candlesPer)
	fmt.Printf("  %s amount:     %.2f-%.2f USDC per side\n", cli.Success("✓"), minAmt, maxAmt)
	fmt.Printf("  %s coverage:   %d/%d (gap=%.2f)\n", cli.Success("✓"), target, visible, gap)

	worstRound := maxAmt * 2 * float64(len(assetNames)) * float64(candlesPer) * float64(len(intervals))
	fmt.Printf("  %s worst-case round size: %.2f USDC\n", cli.Success("✓"), worstRound)

	for _, iv := range intervals {
		halt := time.Duration(float64(candlesPer)*float64(iv)*gap) * time.Second
		fmt.Printf("    halt @ %ds:  %s\n", iv, halt.Round(time.Second))
	}

	// Price feed sanity — fetch prices for every configured asset
	if cfg.HermesURL == "" {
		fmt.Printf("  %s HERMES_URL not set — skipping price fetch\n", cli.Warning("!"))
		return true
	}

	pf := prices.NewFetcher(cfg.HermesURL)
	var priceIDs []string
	for _, name := range assetNames {
		up := strings.ToUpper(name)
		a, ok := cfg.Assets[up]
		if !ok {
			// Viper lowercases keys
			a, ok = cfg.Assets[strings.ToLower(name)]
		}
		if !ok {
			fmt.Printf("    %s no config.assets entry for %s\n", cli.Warning("!"), up)
			continue
		}
		pid := strings.TrimPrefix(a.PriceID, "0x")
		priceIDs = append(priceIDs, pid)
	}

	for c := 1; c <= cycles; c++ {
		fmt.Printf("\n  Cycle %d:\n", c)
		priceMap, perr := pf.FetchFreshPrices(ctx, priceIDs, 30*time.Second, 2)
		if perr != nil {
			fmt.Printf("    %s fetch prices: %v\n", cli.Error("✗"), perr)
			return false
		}
		for id, p := range priceMap {
			converted, cerr := prices.ConvertPythPriceTo1e8(p.Price.Price, p.Price.Expo)
			if cerr != nil {
				fmt.Printf("    id=%s… raw=%s expo=%d age=%ds  %s convert: %v\n",
					short(id), p.Price.Price, p.Price.Expo,
					time.Now().Unix()-p.Price.PublishTime,
					cli.Error("✗"), cerr)
				return false
			}
			fmt.Printf("    id=%s… raw=%s expo=%d → 1e8=%d ($%.4f) age=%ds\n",
				short(id), p.Price.Price, p.Price.Expo,
				converted, float64(converted)/1e8,
				time.Now().Unix()-p.Price.PublishTime)
		}
	}

	fmt.Printf("\n  %s candle-rush: config + price feeds OK\n", cli.Success("✓"))
	return true
}

// reportCoverage prints which enabled assets are present in the Hasura UP_DOWN
// market response. The mismatch shape is the same heuristic the deployed bot
// uses, so this surfaces the BTC: not present in Hasura response variant that
// the FE-vs-bot endpoint mismatch was producing.
func reportCoverage(cfg *config.Config, upDown []markets.Market) {
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
	fmt.Println("  market coverage:")
	var missing []string
	for _, sym := range cfg.Betting.EnabledAssets {
		up := strings.ToUpper(sym)
		if apiSymbols[up] {
			fmt.Printf("    %s present\n", up)
		} else {
			fmt.Printf("    %s %s missing from Hasura\n", up, cli.Warning("!"))
			missing = append(missing, up)
		}
	}
	if len(missing) > 0 {
		fmt.Printf("    %s missing: %s\n", cli.Warning("!"), strings.Join(missing, ", "))
	}
}

func loadConfig(path string) (*config.Config, error) {
	// LoadCandleRush has relaxed validation (no owner key required), which is
	// exactly what we want for a no-secrets test runner.
	return config.LoadCandleRush(path)
}

func expandUrls(cfg *config.Config) {
	if strings.HasPrefix(cfg.HasuraURL, "${") && strings.HasSuffix(cfg.HasuraURL, "}") {
		cfg.HasuraURL = os.Getenv(cfg.HasuraURL[2 : len(cfg.HasuraURL)-1])
	}
	if cfg.HasuraURL == "" {
		cfg.HasuraURL = os.Getenv("HASURA_URL")
	}
	if cfg.HermesURL == "" {
		cfg.HermesURL = os.Getenv("HERMES_URL")
	}
}

func resolveTarget(targetFlag string, nonInteractive bool) int {
	switch strings.ToLower(strings.TrimSpace(targetFlag)) {
	case "both":
		return choiceBoth
	case "price-arena", "price_arena", "bet-bot":
		return choicePriceArena
	case "candle-rush", "candle_rush", "candle-rush-bot":
		return choiceCandleRush
	case "":
	default:
		fmt.Printf("  %s Unknown --target value %q\n", cli.Error("✗"), targetFlag)
		os.Exit(1)
	}

	if nonInteractive || !isInteractiveTerminal() {
		fmt.Printf("  %s --target is required in non-interactive mode\n", cli.Error("✗"))
		os.Exit(1)
	}
	return cli.Choose(
		"Which bot do you want to test?",
		[]string{
			"Both (price arena + candle rush)",
			"Price Arena only",
			"Candle Rush only",
		},
		0,
	)
}

func targetLabel(choice int) string {
	switch choice {
	case choiceBoth:
		return "price-arena + candle-rush"
	case choicePriceArena:
		return "price-arena"
	case choiceCandleRush:
		return "candle-rush"
	}
	return "?"
}

func short(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func maskQuery(u string) string {
	if u == "" {
		return "(not set)"
	}
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i] + "?***"
	}
	return u
}

func successDot(ok bool) string {
	if ok {
		return cli.Success("✓")
	}
	return cli.Error("✗")
}

func isInteractiveTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
