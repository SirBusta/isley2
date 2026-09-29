package handlers

import (
	"reflect"
	"testing"
)

func TestRepairCannadbParentNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want []string
	}{
		// Real CannaDB data for Mango Runtz (Mephisto Genetics).
		{[]string{"(Creme de la Chem", "Mango Smile)", "White Runtz"}, []string{"Creme de la Chem x Mango Smile", "White Runtz"}},
		{[]string{"Zkittlez", "Gelato"}, []string{"Zkittlez", "Gelato"}},
		{[]string{"(A x B)", "C"}, []string{"A x B", "C"}},
		{[]string{" ", "A", ""}, []string{"A"}},
		// Can't pair the brackets: leave as given.
		{[]string{"(A", "B"}, []string{"(A", "B"}},
		{[]string{"A)", "B"}, []string{"A)", "B"}},
	}
	for _, tc := range cases {
		if got := repairCannadbParentNames(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("repairCannadbParentNames(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFindCannadbMatch(t *testing.T) {
	t.Parallel()

	rows := []cannadbSearchResult{
		{URI: "a", Name: "Gelato #33", BreederName: "Cookies"},
		{URI: "b", Name: "Gelato", BreederName: "Cookies"},
		{URI: "c", Name: "Blue Dream", BreederName: "Humboldt"},
		{URI: "d", Name: "blue dream", BreederName: "DJ Short"},
	}
	cases := []struct {
		name, breeder, want string
	}{
		{"Gelato", "anyone", "b"},             // single exact match, breeder irrelevant
		{" gelato ", "", "b"},                 // case and spacing
		{"Blue Dream", "DJ Short", "d"},       // two exact matches, breeder breaks the tie
		{"Blue Dream", "dj short", "d"},       // breeder compared case-insensitively
		{"Blue Dream", "Seed Supreme", ""},    // still ambiguous: no match
		{"Gelato 41", "Cookies", ""},          // no exact match
		{"Permanent Marker", "Seed Junky", ""},
	}
	for _, tc := range cases {
		got := findCannadbMatch(rows, tc.name, tc.breeder)
		gotURI := ""
		if got != nil {
			gotURI = got.URI
		}
		if gotURI != tc.want {
			t.Errorf("findCannadbMatch(%q, %q) = %q, want %q", tc.name, tc.breeder, gotURI, tc.want)
		}
	}
}
