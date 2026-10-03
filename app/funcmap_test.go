package app

import (
	"testing"
	"time"

	"isley/config"
)

func TestSeedAgeFrom(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		added                 string
		ok                    bool
		total, years, months int
	}{
		{"2026-09-20", true, 0, 0, 0},
		{"2026-09-03", true, 1, 0, 1},
		{"2026-09-04", true, 0, 0, 0}, // not a full month yet
		{"2025-03-14", true, 18, 1, 6},
		{"2023-10-03", true, 36, 3, 0},
		{"2027-01-01", true, 0, 0, 0}, // a future date counts as new
		{"", false, 0, 0, 0},
		{"03/14/2025", false, 0, 0, 0},
	}
	for _, tc := range cases {
		got := seedAgeFrom(tc.added, now)
		if got.OK != tc.ok || got.Total != tc.total || got.Years != tc.years || got.Months != tc.months {
			t.Errorf("seedAgeFrom(%q) = %+v, want ok=%v total=%d years=%d months=%d", tc.added, got, tc.ok, tc.total, tc.years, tc.months)
		}
	}
}

func TestDaysToWeeks(t *testing.T) {
	t.Parallel()
	daysToWeeks := buildFuncMap(config.NewStore())["daysToWeeks"].(func(int) string)
	cases := map[int]string{0: "0", 56: "8", 63: "9", 60: "8.6", 59: "8.4", 70: "10", 4: "0.6"}
	for days, want := range cases {
		if got := daysToWeeks(days); got != want {
			t.Errorf("daysToWeeks(%d) = %q, want %q", days, got, want)
		}
	}
}
