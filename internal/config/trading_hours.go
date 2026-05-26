package config

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var tzCache sync.Map // map[string]*time.Location

func loadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	if v, ok := tzCache.Load(name); ok {
		return v.(*time.Location)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	tzCache.Store(name, loc)
	return loc
}

// IsOpen reports whether the asset's market is open at the given instant.
// Returns (true, "") if open or if no schedule is configured. Returns
// (false, reason) with a human-readable reason suitable for logging when closed.
// On any parse failure (bad timezone, bad range), it fails open — we'd rather
// place a bet than silently stall on a config bug.
func (t *TradingHours) IsOpen(now time.Time) (bool, string) {
	if t == nil {
		return true, ""
	}
	loc := loadLocation(t.Timezone)
	if loc == nil {
		return true, ""
	}
	local := now.In(loc)
	schedule := t.scheduleFor(local.Weekday())
	tz := t.Timezone
	if tz == "" {
		tz = "UTC"
	}

	if schedule == "" || strings.EqualFold(schedule, "closed") {
		return false, fmt.Sprintf("closed on %s (%s)", local.Weekday(), tz)
	}

	mins := local.Hour()*60 + local.Minute()
	for _, rng := range strings.Split(schedule, ",") {
		rng = strings.TrimSpace(rng)
		if rng == "" {
			continue
		}
		parts := strings.Split(rng, "-")
		if len(parts) != 2 {
			continue
		}
		start, err1 := parseHHMM(parts[0])
		end, err2 := parseHHMM(parts[1])
		if err1 != nil || err2 != nil {
			continue
		}
		if mins >= start && mins < end {
			return true, ""
		}
	}
	return false, fmt.Sprintf("outside trading hours on %s (schedule: %s %s, local time: %s)",
		local.Weekday(), schedule, tz, local.Format("15:04"))
}

func (t *TradingHours) scheduleFor(day time.Weekday) string {
	switch day {
	case time.Sunday:
		return t.Sunday
	case time.Monday:
		return t.Monday
	case time.Tuesday:
		return t.Tuesday
	case time.Wednesday:
		return t.Wednesday
	case time.Thursday:
		return t.Thursday
	case time.Friday:
		return t.Friday
	case time.Saturday:
		return t.Saturday
	}
	return ""
}

// parseHHMM parses a 4-digit time-of-day "HHMM" into minutes-past-midnight.
// "2400" is accepted as end-of-day (1440).
func parseHHMM(s string) (int, error) {
	s = strings.TrimSpace(s)
	if len(s) != 4 {
		return 0, fmt.Errorf("expected HHMM, got %q", s)
	}
	hh, err := strconv.Atoi(s[:2])
	if err != nil {
		return 0, err
	}
	mm, err := strconv.Atoi(s[2:])
	if err != nil {
		return 0, err
	}
	if hh < 0 || hh > 24 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("time out of range: %q", s)
	}
	return hh*60 + mm, nil
}
