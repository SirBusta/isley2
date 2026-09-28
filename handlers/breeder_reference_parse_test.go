package handlers

import (
	"reflect"
	"testing"
	"time"
)

func TestParseBreederDirectory(t *testing.T) {
	t.Parallel()

	page := `<html><body>
		<a href="/breeders/Sensi%20Seeds">Sensi Seeds</a>
		<a href="/breeders/Sensi%20Seeds">Sensi Seeds (featured)</a>
		<a href="/breeders/Barney%27s%20Farm">Barney's Farm</a>
		<a href="/breeders/Cookies%20%26amp%3B%20Cream">x</a>
		<a href="/breeders/Salt%20&amp;%20Pepper">x</a>
		<a href="/breeders/Doctor%C3%A2%C2%80%C2%99s%20Choice%20Seeds">mojibake</a>
		<a href="/breeders/Caf%C3%A9%20Genetics">real accent</a>
		<a href="/breeders/NULL">null</a>
		<a href="/breeders/Unknown%20Breeder">unknown</a>
		<a href="/breeders?page=2">next page</a>
		<a href="/breeders/Sensi%20Seeds/strains">sub page</a>
		<a href="/strains/blue-dream">a strain</a>
		<a href="/breeders/%20Padded%20">padded</a>
	</body></html>`

	got := parseBreederDirectory(page)
	want := []string{
		"Sensi Seeds",
		"Barney's Farm",
		"Cookies &amp; Cream",
		"Salt & Pepper",
		"Doctor’s Choice Seeds",
		"Café Genetics",
		"Padded",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBreederDirectory:\n got  %q\n want %q", got, want)
	}
}

func TestRepairMojibake(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Doctorâ\u0080\u0099s": "Doctor’s",
		"Café":                      "Café", // genuine Latin-1 text isn't valid UTF-8 once re-encoded
		"Plain ASCII":               "Plain ASCII",
		"Already ’ fine":            "Already ’ fine",
	}
	for in, want := range cases {
		if got := repairMojibake(in); got != want {
			t.Errorf("repairMojibake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBreederRefDueAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }

	cases := []struct {
		name, lastAttempt, lastError string
		want                         bool
	}{
		{"never attempted", "", "", true},
		{"unparseable timestamp", "yesterday", "", true},
		{"succeeded 10 days ago", ago(10 * 24 * time.Hour), "", false},
		{"succeeded 30 days ago", ago(30 * 24 * time.Hour), "", true},
		{"failed 3 days ago", ago(3 * 24 * time.Hour), "boom", false},
		{"failed 7 days ago", ago(7 * 24 * time.Hour), "boom", true},
	}
	for _, tc := range cases {
		if got := breederRefDueAt(tc.lastAttempt, tc.lastError, now); got != tc.want {
			t.Errorf("%s: breederRefDueAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}
