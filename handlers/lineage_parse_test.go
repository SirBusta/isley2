package handlers

import (
	"reflect"
	"testing"
)

func TestParseLineageNote(t *testing.T) {
	t.Parallel()

	cases := []struct {
		note   string
		want   []string
		wantOK bool
	}{
		{"Biscotti x Sherb Bx", []string{"Biscotti", "Sherb Bx"}, true},
		{"OG Kush X Durban Poison", []string{"OG Kush", "Durban Poison"}, true},
		{"Zkittlez × Gelato", []string{"Zkittlez", "Gelato"}, true},
		{"A x B x C", []string{"A", "B", "C"}, true},
		{"Runtz x (Zkittlez x Gelato)", []string{"Runtz", "Zkittlez x Gelato"}, true},
		{"(Creme de la Chem x Mango Smile) x White Runtz", []string{"Creme de la Chem x Mango Smile", "White Runtz"}, true},
		{"Neville's Wreck x Amnesia Haze", []string{"Neville's Wreck", "Amnesia Haze"}, true},
		{"Chemdawg x Unknown", []string{"Chemdawg"}, true},
		{"Gelato x gelato", []string{"Gelato"}, true},
		{"  Sour Diesel   x   Blueberry  ", []string{"Sour Diesel", "Blueberry"}, true},
		{"Xanadu x Blue Dream", []string{"Xanadu", "Blue Dream"}, true},

		// Not confident enough to record anything.
		{"", nil, false},
		{"Blue Dream S1", nil, false},
		{"Runtz x (Zkittlez x Gelato", nil, false},
		{"Runtz) x Gelato", nil, false},
		{"Project X x Blue Dream", nil, false},
		{"Runtz x ", nil, false},
		{"Unknown x Unknown", nil, false},
	}
	for _, tc := range cases {
		got, ok := parseLineageNote(tc.note)
		if ok != tc.wantOK || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseLineageNote(%q) = %q, %v; want %q, %v", tc.note, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestStripOuterParens(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"(B x C)":       "B x C",
		"[B x C]":       "B x C",
		"( B x C )":     "B x C",
		"(A) B (C)":     "(A) B (C)",
		"No parens":     "No parens",
		"((A x B) x C)": "(A x B) x C",
	}
	for in, want := range cases {
		if got := stripOuterParens(in); got != want {
			t.Errorf("stripOuterParens(%q) = %q, want %q", in, got, want)
		}
	}
}
