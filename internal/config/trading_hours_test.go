package config

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func TestIsOpenNilAlways24_7(t *testing.T) {
	var th *TradingHours
	open, _ := th.IsOpen(time.Now())
	if !open {
		t.Fatal("nil TradingHours should report open")
	}
}

func TestIsOpenCommoditySchedule(t *testing.T) {
	th := &TradingHours{
		Timezone:  "America/New_York",
		Sunday:    "1800-2400",
		Monday:    "0000-1700,1800-2400",
		Tuesday:   "0000-1700,1800-2400",
		Wednesday: "0000-1700,1800-2400",
		Thursday:  "0000-1700,1800-2400",
		Friday:    "0000-1700",
		Saturday:  "closed",
	}
	ny := mustLoc(t, "America/New_York")
	cases := []struct {
		name   string
		when   time.Time
		wantOk bool
	}{
		{"Sat noon closed", time.Date(2026, 4, 25, 12, 0, 0, 0, ny), false},
		{"Sun 17:59 closed", time.Date(2026, 4, 26, 17, 59, 0, 0, ny), false},
		{"Sun 18:00 open", time.Date(2026, 4, 26, 18, 0, 0, 0, ny), true},
		{"Sun 23:59 open", time.Date(2026, 4, 26, 23, 59, 0, 0, ny), true},
		{"Mon 09:00 open", time.Date(2026, 4, 27, 9, 0, 0, 0, ny), true},
		{"Mon 17:00 daily break", time.Date(2026, 4, 27, 17, 0, 0, 0, ny), false},
		{"Mon 17:30 daily break", time.Date(2026, 4, 27, 17, 30, 0, 0, ny), false},
		{"Mon 18:00 reopen", time.Date(2026, 4, 27, 18, 0, 0, 0, ny), true},
		{"Fri 16:59 open", time.Date(2026, 4, 24, 16, 59, 0, 0, ny), true},
		{"Fri 17:00 closed", time.Date(2026, 4, 24, 17, 0, 0, 0, ny), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := th.IsOpen(c.when)
			if got != c.wantOk {
				t.Fatalf("IsOpen(%s) = %v (reason=%q); want %v", c.when, got, reason, c.wantOk)
			}
		})
	}
}

func TestIsOpenEquitySchedule(t *testing.T) {
	th := &TradingHours{
		Timezone:  "America/New_York",
		Sunday:    "closed",
		Monday:    "0930-1600",
		Tuesday:   "0930-1600",
		Wednesday: "0930-1600",
		Thursday:  "0930-1600",
		Friday:    "0930-1600",
		Saturday:  "closed",
	}
	ny := mustLoc(t, "America/New_York")
	cases := []struct {
		name   string
		when   time.Time
		wantOk bool
	}{
		{"Mon 08:00 pre-market", time.Date(2026, 4, 27, 8, 0, 0, 0, ny), false},
		{"Mon 09:29 pre-market", time.Date(2026, 4, 27, 9, 29, 0, 0, ny), false},
		{"Mon 09:30 open", time.Date(2026, 4, 27, 9, 30, 0, 0, ny), true},
		{"Mon 15:59 open", time.Date(2026, 4, 27, 15, 59, 0, 0, ny), true},
		{"Mon 16:00 closed", time.Date(2026, 4, 27, 16, 0, 0, 0, ny), false},
		{"Sat closed", time.Date(2026, 4, 25, 12, 0, 0, 0, ny), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := th.IsOpen(c.when)
			if got != c.wantOk {
				t.Fatalf("IsOpen(%s) = %v (reason=%q); want %v", c.when, got, reason, c.wantOk)
			}
		})
	}
}

func TestIsOpenMalformedFailsOpen(t *testing.T) {
	th := &TradingHours{
		Timezone: "Not/A_Zone",
		Monday:   "garbage",
	}
	// Bad timezone → fail open.
	if ok, _ := th.IsOpen(time.Now()); !ok {
		t.Fatal("bad timezone should fail open")
	}
}

func TestParseHHMM(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"0000", 0, false},
		{"0930", 570, false},
		{"1600", 960, false},
		{"2400", 1440, false},
		{"2500", 0, true},
		{"1260", 0, true},
		{"abc", 0, true},
		{"930", 0, true},
	}
	for _, c := range cases {
		got, err := parseHHMM(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseHHMM(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("parseHHMM(%q) = %d; want %d", c.in, got, c.want)
		}
	}
}
