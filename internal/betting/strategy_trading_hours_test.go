package betting

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
)

// captureStdout runs fn while redirecting os.Stdout to a buffer; returns captured output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func newTestStrategy() *Strategy {
	ny := "America/New_York"
	return NewStrategy(
		config.BettingConfig{
			EnabledAssets:     []string{"BTC", "AAPL", "GOLD"},
			EnabledTimeframes: []string{"5m"},
			MinAmountUSDC:     0.1,
			MaxAmountUSDC:     1,
		},
		map[string]config.Asset{
			"BTC": {
				Address: "0xBbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				PriceID: "0xbbbb",
			},
			"AAPL": {
				Address: "0xAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				PriceID: "0xaaaa",
				TradingHours: &config.TradingHours{
					Timezone: ny, Sunday: "closed", Monday: "0930-1600", Tuesday: "0930-1600",
					Wednesday: "0930-1600", Thursday: "0930-1600", Friday: "0930-1600", Saturday: "closed",
				},
			},
			"GOLD": {
				Address: "0xGggggggggggggggggggggggggggggggggggggggg",
				PriceID: "0xgggg",
				TradingHours: &config.TradingHours{
					Timezone: ny, Sunday: "1800-2400", Monday: "0000-1700,1800-2400",
					Tuesday: "0000-1700,1800-2400", Wednesday: "0000-1700,1800-2400",
					Thursday: "0000-1700,1800-2400", Friday: "0000-1700", Saturday: "closed",
				},
			},
		},
	)
}

func upDown(addr string) markets.Market {
	b := addr
	return markets.Market{
		Kind: markets.MarketKindUpDown,
		Base: &b,
		Periods: []markets.Period{
			{Period: "1", PeriodSeconds: "300", Status: markets.PeriodStatusAvailable},
		},
		PeriodKeys: []string{"1"},
	}
}

func relative(addrs ...string) markets.Market {
	return markets.Market{
		Kind:   markets.MarketKindRelative,
		Assets: addrs,
		Periods: []markets.Period{
			{Period: "1", PeriodSeconds: "300", Status: markets.PeriodStatusAvailable},
		},
		PeriodKeys: []string{"1"},
	}
}

func TestFilter_SkipsClosedUpDownAndLogsOnce(t *testing.T) {
	s := newTestStrategy()
	ny, _ := time.LoadLocation("America/New_York")
	// Saturday noon ET: AAPL+GOLD closed, BTC always open.
	sat := time.Date(2026, 4, 25, 12, 0, 0, 0, ny)

	btc := upDown("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	aapl := upDown("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	gold := upDown("0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG")
	// Duplicate AAPL market — log must still appear only once.
	aapl2 := upDown("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	mkts := []markets.Market{btc, aapl, gold, aapl2}

	var out string
	var valid []markets.Market
	out = captureStdout(t, func() {
		valid = s.filterUpDownMarkets(mkts, sat, make(map[string]bool))
	})

	if len(valid) != 1 {
		t.Fatalf("expected 1 valid market (BTC), got %d", len(valid))
	}
	if *valid[0].Base != "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB" {
		t.Fatalf("expected BTC market, got %s", *valid[0].Base)
	}
	if !strings.Contains(out, "skipping AAPL markets") {
		t.Errorf("missing AAPL-closed log line; got:\n%s", out)
	}
	if !strings.Contains(out, "skipping GOLD markets") {
		t.Errorf("missing GOLD-closed log line; got:\n%s", out)
	}
	if strings.Count(out, "skipping AAPL markets") != 1 {
		t.Errorf("AAPL log should appear exactly once; got:\n%s", out)
	}
}

func TestFilter_RelativeRequiresAllOpen(t *testing.T) {
	s := newTestStrategy()
	ny, _ := time.LoadLocation("America/New_York")
	sat := time.Date(2026, 4, 25, 12, 0, 0, 0, ny)

	// BTC vs GOLD: GOLD closed on Sat → whole market skipped.
	m := relative("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", "0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG")
	valid := s.filterRelativeMarkets([]markets.Market{m}, sat, make(map[string]bool))
	if len(valid) != 0 {
		t.Fatalf("expected 0 valid relative markets (GOLD closed), got %d", len(valid))
	}
}

func TestFilter_AllOpenDuringEquityHours(t *testing.T) {
	s := newTestStrategy()
	ny, _ := time.LoadLocation("America/New_York")
	// Monday 10:00 ET: all open.
	monOpen := time.Date(2026, 4, 27, 10, 0, 0, 0, ny)

	mkts := []markets.Market{
		upDown("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"),
		upDown("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
		upDown("0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG"),
	}
	out := captureStdout(t, func() {
		valid := s.filterUpDownMarkets(mkts, monOpen, make(map[string]bool))
		if len(valid) != 3 {
			t.Fatalf("expected 3 valid markets during open hours, got %d", len(valid))
		}
	})
	if strings.Contains(out, "skipping") {
		t.Errorf("unexpected skip log during open hours:\n%s", out)
	}
}
