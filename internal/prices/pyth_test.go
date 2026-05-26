package prices

import "testing"

// TestConvertPythPriceTo1e8 pins the conversion across the three expo regimes
// the bot actually sees in production:
//   - expo == -8 (crypto majors: BTC, ETH, SOL) → no-op
//   - expo > -8  (commodities, equities)        → multiply
//   - expo < -8  (hypothetical finer feed)      → divide (truncates)
//
// The bug we are guarding against here is the historical inversion of the
// multiply/divide branches, which produced on-chain prices off by 10^|diff|
// for every non-crypto asset.
func TestConvertPythPriceTo1e8(t *testing.T) {
	tests := []struct {
		name  string
		price string
		expo  int
		want  uint64
	}{
		// BTC ~ $77,386.18 — feed already at 1e8 scale
		{"btc_expo_neg8", "7738618858216", -8, 7738618858216},
		// GOLD ~ $4,534.873 with expo=-3 → must multiply by 10^5
		{"gold_expo_neg3", "4534873", -3, 453487300000},
		// SILVER ~ $76.35482 with expo=-5 → must multiply by 10^3
		{"silver_expo_neg5", "7635482", -5, 7635482000},
		// WTI ~ $91.16963 with expo=-5 → must multiply by 10^3
		{"wti_expo_neg5", "9116963", -5, 9116963000},
		// A finer-than-target feed at expo=-10 → divide by 10^2 (truncates)
		{"finer_expo_neg10_truncates", "1234567890123", -10, 12345678901},
		// Round number with expo=0 ($100.00000000)
		{"int_dollar_expo_0", "100", 0, 10000000000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ConvertPythPriceTo1e8(tc.price, tc.expo)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ConvertPythPriceTo1e8(%q, %d) = %d, want %d",
					tc.price, tc.expo, got, tc.want)
			}
		})
	}
}

func TestConvertPythPriceTo1e8_RejectsBadInput(t *testing.T) {
	if _, err := ConvertPythPriceTo1e8("not-a-number", -8); err == nil {
		t.Error("expected error for non-numeric input, got nil")
	}
	if _, err := ConvertPythPriceTo1e8("-1", -8); err == nil {
		t.Error("expected error for negative price, got nil")
	}
}
