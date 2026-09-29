package handlers

import (
	"database/sql"
	"strings"
)

// findCannadbMatch picks the CannaDB search row describing the same strain:
// an exact (case-insensitive) name match, using the breeder to choose
// between several. It returns nil when there is no match or the choice is
// still ambiguous, because filling in another strain's data would be worse
// than filling in nothing.
func findCannadbMatch(rows []cannadbSearchResult, name, breeder string) *cannadbSearchResult {
	name = strings.TrimSpace(name)
	var exact []*cannadbSearchResult
	for i := range rows {
		if strings.EqualFold(strings.TrimSpace(rows[i].Name), name) {
			exact = append(exact, &rows[i])
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	var byBreeder []*cannadbSearchResult
	for _, r := range exact {
		if strings.EqualFold(strings.TrimSpace(r.BreederName), strings.TrimSpace(breeder)) {
			byBreeder = append(byBreeder, r)
		}
	}
	if len(byBreeder) == 1 {
		return byBreeder[0]
	}
	return nil
}

// repairCannadbParentNames rejoins a bracketed cross that CannaDB split on
// "x" into separate parents: ["(Creme de la Chem", "Mango Smile)", "White
// Runtz"] becomes ["Creme de la Chem x Mango Smile", "White Runtz"]. Names
// are returned unchanged if the brackets can't be paired up.
func repairCannadbParentNames(names []string) []string {
	var out []string
	var group []string
	depth := 0
	for _, raw := range names {
		n := strings.TrimSpace(raw)
		if n == "" {
			continue
		}
		depth += strings.Count(n, "(") - strings.Count(n, ")")
		if depth < 0 {
			return names
		}
		group = append(group, n)
		if depth == 0 {
			out = append(out, stripOuterParens(strings.Join(group, " x ")))
			group = nil
		}
	}
	if depth != 0 {
		return names
	}
	return out
}

// enrichFromCannadb fills gaps in a strain just imported from StrainCompass
// using the matching CannaDB record: today, the parent strains when the
// strain has none. It never replaces existing data. It returns the matched
// record (nil when there was none) so callers can offer its other content.
func enrichFromCannadb(db *sql.DB, baseURL string, strainID int, name, breeder string) (*cannadbRecord, *cannadbStrainValue, error) {
	rows, err := cannadbSearchStrains(baseURL, name, 10)
	if err != nil {
		return nil, nil, err
	}
	match := findCannadbMatch(rows, name, breeder)
	if match == nil {
		return nil, nil, nil
	}
	rec, val, err := cannadbGetStrain(baseURL, match.URI)
	if err != nil {
		return nil, nil, err
	}
	if len(val.ParentNames) > 0 {
		if _, err := seedLineage(db, strainID, repairCannadbParentNames(val.ParentNames), lineageSourceCannadb, rec.URI); err != nil {
			return rec, val, err
		}
	}
	return rec, val, nil
}
