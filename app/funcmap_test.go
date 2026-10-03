package app

import (
	"testing"

	"isley/config"
)

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
