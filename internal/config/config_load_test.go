package config

import (
	"os"
	"testing"
)

// TestLoadRealConfig verifies that the repo's config.yaml parses cleanly and
// that trading_hours YAML anchors (&us_equity_hours, &commodity_hours) are
// expanded into the correct per-asset structs.
func TestLoadRealConfig(t *testing.T) {
	os.Setenv("OWNER_PRIVATE_KEY", "0x0000000000000000000000000000000000000000000000000000000000000001")
	os.Setenv("HASURA_URL", "http://test")
	os.Setenv("HERMES_URL", "http://test")
	defer os.Unsetenv("OWNER_PRIVATE_KEY")
	defer os.Unsetenv("HASURA_URL")
	defer os.Unsetenv("HERMES_URL")

	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Viper lowercases YAML keys, so Assets is keyed by lowercase symbol.
	wantAssets := []string{"btc", "eth", "sol", "aapl", "amzn", "tsla", "nvda", "goog", "meta", "nflx", "gold", "silver", "wti"}
	for _, s := range wantAssets {
		if _, ok := cfg.Assets[s]; !ok {
			t.Errorf("missing asset %s", s)
		}
	}

	// Crypto → no trading_hours
	for _, s := range []string{"btc", "eth", "sol"} {
		if cfg.Assets[s].TradingHours != nil {
			t.Errorf("%s: expected nil TradingHours (24/7), got %+v", s, cfg.Assets[s].TradingHours)
		}
	}

	// Equities → 0930-1600 Mon-Fri
	for _, s := range []string{"aapl", "amzn", "tsla", "nvda", "goog", "meta", "nflx"} {
		th := cfg.Assets[s].TradingHours
		if th == nil {
			t.Errorf("%s: expected TradingHours, got nil", s)
			continue
		}
		if th.Timezone != "America/New_York" {
			t.Errorf("%s: timezone = %q, want America/New_York", s, th.Timezone)
		}
		if th.Monday != "0930-1600" {
			t.Errorf("%s: Monday = %q, want 0930-1600", s, th.Monday)
		}
		if th.Saturday != "closed" {
			t.Errorf("%s: Saturday = %q, want closed", s, th.Saturday)
		}
	}

	// Commodities → split schedule, Fri closes at 17:00
	for _, s := range []string{"gold", "silver", "wti"} {
		th := cfg.Assets[s].TradingHours
		if th == nil {
			t.Errorf("%s: expected TradingHours, got nil", s)
			continue
		}
		if th.Monday != "0000-1700,1800-2400" {
			t.Errorf("%s: Monday = %q, want 0000-1700,1800-2400", s, th.Monday)
		}
		if th.Friday != "0000-1700" {
			t.Errorf("%s: Friday = %q, want 0000-1700", s, th.Friday)
		}
		if th.Sunday != "1800-2400" {
			t.Errorf("%s: Sunday = %q, want 1800-2400", s, th.Sunday)
		}
	}
}

func TestAssetKeys(t *testing.T) {
	os.Setenv("OWNER_PRIVATE_KEY", "0x0000000000000000000000000000000000000000000000000000000000000001")
	os.Setenv("HASURA_URL", "http://test")
	os.Setenv("HERMES_URL", "http://test")
	defer os.Unsetenv("OWNER_PRIVATE_KEY")
	defer os.Unsetenv("HASURA_URL")
	defer os.Unsetenv("HERMES_URL")
	cfg, err := Load("../../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for k := range cfg.Assets {
		t.Logf("asset key: %q", k)
	}
}
