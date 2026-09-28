package handlers

import (
	"database/sql"
	"fmt"
	"strings"

	"isley/model/types"
	"isley/utils"
)

// maxStrainAttributesPerGroup caps how many effects/flavors/etc. one strain
// can carry, so a bad request can't insert thousands of rows.
const maxStrainAttributesPerGroup = 50

// strainAttributeSet holds replacement lists for a strain's attribute
// groups. A nil group is left untouched; a non-nil empty group is cleared.
type strainAttributeSet struct {
	Effects     *[]types.StrainAttribute
	Flavors     *[]types.StrainAttribute
	Terpenes    *[]types.StrainAttribute
	MedicalUses *[]types.StrainAttribute
}

// replaceStrainAttributes rewrites each non-nil group for the strain. Names
// are trimmed, blanks dropped, and duplicates (case-insensitive) collapsed
// to their first occurrence.
func replaceStrainAttributes(tx *sql.Tx, strainID int, set strainAttributeSet) error {
	groups := []struct {
		table string
		attrs *[]types.StrainAttribute
	}{
		{"strain_effect", set.Effects},
		{"strain_flavor", set.Flavors},
		{"strain_terpene", set.Terpenes},
		{"strain_medical_use", set.MedicalUses},
	}
	for _, g := range groups {
		if g.attrs == nil {
			continue
		}
		if _, err := tx.Exec("DELETE FROM "+g.table+" WHERE strain_id = $1", strainID); err != nil { //nolint:gosec // table names are constants above
			return err
		}
		seen := map[string]bool{}
		for _, a := range *g.attrs {
			name := strings.TrimSpace(a.Name)
			key := strings.ToLower(name)
			if name == "" || seen[key] {
				continue
			}
			seen[key] = true
			var err error
			switch g.table {
			case "strain_effect":
				_, err = tx.Exec("INSERT INTO strain_effect (strain_id, name, intensity) VALUES ($1, $2, $3)", strainID, name, a.Intensity)
			case "strain_terpene":
				_, err = tx.Exec("INSERT INTO strain_terpene (strain_id, name, level) VALUES ($1, $2, $3)", strainID, name, a.Level)
			default:
				_, err = tx.Exec("INSERT INTO "+g.table+" (strain_id, name) VALUES ($1, $2)", strainID, name) //nolint:gosec // constant table name
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// validateStrainAttributeGroup checks one user-supplied group, returning an
// API error key (or a utils validation message) when it's unacceptable.
func validateStrainAttributeGroup(field string, attrs []types.StrainAttribute) error {
	if len(attrs) > maxStrainAttributesPerGroup {
		return fmt.Errorf("api_too_many_strain_attributes")
	}
	for _, a := range attrs {
		if err := utils.ValidateStringLength(field, strings.TrimSpace(a.Name), utils.MaxNameLength); err != nil {
			return err
		}
		if a.Level != nil {
			if err := utils.ValidateStringLength(field+"_level", *a.Level, utils.MaxNameLength); err != nil {
				return err
			}
		}
	}
	return nil
}

// strainGrowingInfo is the editable height/yield block; empty strings are
// stored as NULL.
type strainGrowingInfo struct {
	HeightIndoor  string `json:"height_indoor"`
	HeightOutdoor string `json:"height_outdoor"`
	YieldIndoor   string `json:"yield_indoor"`
	YieldOutdoor  string `json:"yield_outdoor"`
}

// normalize trims every field and rejects overlong values.
func (g *strainGrowingInfo) normalize() error {
	for _, f := range []struct {
		name string
		val  *string
	}{
		{"height_indoor", &g.HeightIndoor}, {"height_outdoor", &g.HeightOutdoor},
		{"yield_indoor", &g.YieldIndoor}, {"yield_outdoor", &g.YieldOutdoor},
	} {
		*f.val = strings.TrimSpace(*f.val)
		if err := utils.ValidateStringLength(f.name, *f.val, utils.MaxNameLength); err != nil {
			return err
		}
	}
	return nil
}

// strainCannabinoids is the editable cannabinoid block; nil fields are
// stored as NULL (unknown).
type strainCannabinoids struct {
	ThcMin *float64 `json:"thc_min"`
	ThcMax *float64 `json:"thc_max"`
	CbdMin *float64 `json:"cbd_min"`
	CbdMax *float64 `json:"cbd_max"`
	CbnMax *float64 `json:"cbn_max"`
	CbgMax *float64 `json:"cbg_max"`
}

// validate returns an API error key when a value is outside 0-100 or a
// minimum exceeds its maximum.
func (c strainCannabinoids) validate() string {
	for _, v := range []*float64{c.ThcMin, c.ThcMax, c.CbdMin, c.CbdMax, c.CbnMax, c.CbgMax} {
		if v != nil && (*v < 0 || *v > 100) {
			return "api_cannabinoid_out_of_range"
		}
	}
	if c.ThcMin != nil && c.ThcMax != nil && *c.ThcMin > *c.ThcMax {
		return "api_cannabinoid_min_over_max"
	}
	if c.CbdMin != nil && c.CbdMax != nil && *c.CbdMin > *c.CbdMax {
		return "api_cannabinoid_min_over_max"
	}
	return ""
}
