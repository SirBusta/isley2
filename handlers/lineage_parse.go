package handlers

import (
	"database/sql"
	"strings"
	"unicode"

	"isley/utils"
)

// parseLineageNote splits a source's flat lineage text, e.g.
// "Biscotti x Sherb Bx", into parent names. It splits only on top-level
// " x " / " X " / " × " separators, never inside parentheses, so
// "A x (B x C)" yields "A" and the cross "B x C" as one parent. It reports
// ok=false (record nothing) when the text can't be read confidently:
// unbalanced brackets, an empty part, or no separator at all.
func parseLineageNote(note string) (parents []string, ok bool) {
	runes := []rune(strings.TrimSpace(note))
	if len(runes) == 0 {
		return nil, false
	}

	var parts []string
	depth, start := 0, 0
	for i, r := range runes {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
			if depth < 0 {
				return nil, false
			}
		case 'x', 'X', '×':
			if depth == 0 && i > 0 && i < len(runes)-1 &&
				unicode.IsSpace(runes[i-1]) && unicode.IsSpace(runes[i+1]) {
				parts = append(parts, string(runes[start:i]))
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	parts = append(parts, string(runes[start:]))
	if len(parts) < 2 {
		return nil, false
	}

	seen := map[string]bool{}
	for _, p := range parts {
		p = stripOuterParens(strings.TrimSpace(p))
		if p == "" {
			return nil, false
		}
		if strings.EqualFold(p, "unknown") || p == "?" {
			continue
		}
		if key := strings.ToLower(p); !seen[key] {
			seen[key] = true
			parents = append(parents, p)
		}
	}
	if len(parents) == 0 {
		return nil, false
	}
	return parents, true
}

// stripOuterParens removes one pair of brackets wrapping the whole string:
// "(B x C)" -> "B x C", but "(A) B (C)" is left as is.
func stripOuterParens(s string) string {
	runes := []rune(s)
	if len(runes) < 2 || !((runes[0] == '(' && runes[len(runes)-1] == ')') || (runes[0] == '[' && runes[len(runes)-1] == ']')) {
		return s
	}
	depth := 0
	for i, r := range runes {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
			if depth == 0 && i != len(runes)-1 {
				return s
			}
		}
	}
	return strings.TrimSpace(string(runes[1 : len(runes)-1]))
}

// seedLineageFromNote records the parents parsed from a source lineage note,
// but only when the strain has no lineage yet, so a user's own lineage is
// never overwritten. A parent is linked when exactly one other local strain
// has that name. Returns the number of parents recorded.
func seedLineageFromNote(db *sql.DB, strainID int, note string) (int, error) {
	parents, ok := parseLineageNote(note)
	if !ok {
		return 0, nil
	}

	var existing int
	if err := db.QueryRow("SELECT COUNT(*) FROM strain_lineage WHERE strain_id = $1", strainID).Scan(&existing); err != nil {
		return 0, err
	}
	if existing > 0 {
		return 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	added := 0
	for _, name := range parents {
		if len(name) > utils.MaxNameLength {
			continue
		}
		linkID, err := uniqueLocalStrainID(tx, name, strainID)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(
			"INSERT INTO strain_lineage (strain_id, parent_name, parent_strain_id) VALUES ($1, $2, $3)",
			strainID, name, linkID); err != nil {
			return 0, err
		}
		added++
	}
	return added, tx.Commit()
}

// uniqueLocalStrainID returns the id of the only strain (other than
// excludeID) named name, case-insensitively, or nil when there are none or
// several (the same strain from two breeders).
func uniqueLocalStrainID(tx *sql.Tx, name string, excludeID int) (any, error) {
	rows, err := tx.Query("SELECT id FROM strain WHERE LOWER(name) = LOWER($1) AND id <> $2 LIMIT 2", name, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		return nil, nil
	}
	return ids[0], nil
}
