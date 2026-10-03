package handlers

import "testing"

func TestResolveStrainStock(t *testing.T) {
	t.Parallel()
	const today = "2026-10-03"
	str := func(s string) *string { return &s }
	yes, no := true, false
	stocked := strainStockPrev{Count: 5, AddedOn: "2025-01-10"}
	stockedNoDate := strainStockPrev{Count: 5}
	empty := strainStockPrev{Count: 0, Wanted: true}

	cases := []struct {
		name       string
		count      int
		wanted     *bool
		addedOn    *string
		prev       strainStockPrev
		wantWanted int
		wantDate   any
	}{
		{"new strain with seeds, no date: today", 10, &no, str(""), newStrainStock, 0, today},
		{"new strain with seeds, date given", 10, &no, str("2024-05-01"), newStrainStock, 0, "2024-05-01"},
		{"new strain with seeds can't be wanted", 10, &yes, str(""), newStrainStock, 0, today},
		{"new strain, no seeds, wanted", 0, &yes, str("2024-05-01"), newStrainStock, 1, nil},
		{"restock from zero: today", 3, nil, nil, empty, 0, today},
		{"restock from zero, date given", 3, &yes, str("2026-09-01"), empty, 0, "2026-09-01"},
		{"still in stock, date not sent: kept", 4, nil, nil, stocked, 0, "2025-01-10"},
		{"still in stock, date changed", 4, &no, str("2025-02-02"), stocked, 0, "2025-02-02"},
		{"still in stock, date cleared by the user", 4, &no, str(""), stocked, 0, nil},
		{"in stock with no recorded date stays unknown", 4, &no, str(""), stockedNoDate, 0, nil},
		{"used up: date cleared, wanted as sent", 0, &yes, str("2025-01-10"), stocked, 1, nil},
		{"no seeds, wanted not sent: kept", 0, nil, nil, empty, 1, nil},
	}
	for _, tc := range cases {
		got, err := resolveStrainStock(tc.count, tc.wanted, tc.addedOn, tc.prev, today)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if got.Wanted != tc.wantWanted || got.AddedOn != tc.wantDate {
			t.Errorf("%s: got wanted=%d date=%v, want wanted=%d date=%v", tc.name, got.Wanted, got.AddedOn, tc.wantWanted, tc.wantDate)
		}
	}

	if _, err := resolveStrainStock(1, nil, str("10/03/2026"), newStrainStock, today); err != errInvalidSeedsAddedOn {
		t.Errorf("bad date: got %v, want errInvalidSeedsAddedOn", err)
	}
}
