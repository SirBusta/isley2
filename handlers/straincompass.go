package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"isley/logger"
	"isley/model/types"
	"isley/utils"
)

// ---------------------------------------------------------------------------
// StrainCompass import handlers.
//
// Same one-click search-and-import pattern as CannaDB: the user searches
// StrainCompass by name and imports a chosen strain into Isley's local
// library. Records are keyed on the StrainCompass slug (straincompass_slug)
// so a re-import upserts in place rather than duplicating. Unlike CannaDB,
// StrainCompass also carries cannabinoid/effects/flavor/terpene data, which
// this integration imports alongside the base strain fields.
// ---------------------------------------------------------------------------

// straincompassMinQueryLen mirrors the API's documented 2-character search
// minimum; enforced client-side too so a too-short query fails fast instead
// of round-tripping a guaranteed error.
const straincompassMinQueryLen = 2

// straincompassEnabled reports whether the integration is switched on.
func straincompassEnabled(c *gin.Context) bool {
	return ConfigStoreFromContext(c).StraincompassEnabled() == 1
}

// StraincompassSearchHandler proxies the autocomplete search endpoint.
// GET /strains/straincompass/search?q=<name>&limit=<n>
func StraincompassSearchHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "StraincompassSearchHandler")

	if !straincompassEnabled(c) {
		apiBadRequest(c, "api_straincompass_disabled")
		return
	}

	query := strings.TrimSpace(c.Query("q"))
	if len(query) < straincompassMinQueryLen {
		apiBadRequest(c, "api_straincompass_query_too_short")
		return
	}

	limit := 10
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= straincompassKeylessResultCap {
			limit = n
		}
	}

	store := ConfigStoreFromContext(c)
	results, err := straincompassSearchStrains(store.StraincompassBaseURL(), store.StraincompassAPIKey(), query, limit)
	if err != nil {
		respondStraincompassError(c, err, "api_straincompass_search_failed")
		return
	}

	fieldLogger.WithField("count", len(results)).Debug("StrainCompass search complete")
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// StraincompassImportHandler fetches a full strain record and upserts it
// locally, including its cannabinoid/effects/flavor/terpene data.
// POST /strains/straincompass/import  {"slug": "<slug>"}
func StraincompassImportHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "StraincompassImportHandler")

	if !straincompassEnabled(c) {
		apiBadRequest(c, "api_straincompass_disabled")
		return
	}

	var req struct {
		Slug string `json:"slug"`
		// Name is optional but strongly recommended: the list/filter
		// endpoint used to resolve the full record matches on the strain's
		// display name, not its slug, so without a name a multi-word slug
		// (e.g. "permanent-marker") won't resolve. The search results the
		// frontend renders already carry the name, so it's sent along.
		Name string `json:"name"`
		// The breeder the user bought from. StrainCompass's own breeder is
		// per listing and often a reseller, so the user's choice wins; when
		// neither is sent, the listing's breeder is used.
		BreederID  *int   `json:"breeder_id"`
		NewBreeder string `json:"new_breeder"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiBadRequest(c, "api_invalid_request_payload")
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Slug == "" {
		apiBadRequest(c, "api_straincompass_invalid_slug")
		return
	}
	if err := utils.ValidateStringLength("new_breeder", req.NewBreeder, utils.MaxNameLength); err != nil {
		apiBadRequest(c, err.Error())
		return
	}

	db := DBFromContext(c)
	store := ConfigStoreFromContext(c)
	baseURL := store.StraincompassBaseURL()
	apiKey := store.StraincompassAPIKey()

	rec, err := straincompassGetStrainBySlug(baseURL, apiKey, req.Slug, req.Name)
	if err != nil {
		respondStraincompassError(c, err, "api_straincompass_import_failed")
		return
	}
	if rec.Name == "" {
		apiInternalError(c, "api_straincompass_import_failed")
		return
	}

	breederID, err := resolveImportBreeder(db, req.BreederID, req.NewBreeder, func() (int, error) {
		return importStraincompassBreeder(db, rec.Breeder)
	})
	if errors.Is(err, errImportBreederNotFound) {
		apiBadRequest(c, "api_breeder_not_found")
		return
	}
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to resolve breeder")
		apiInternalError(c, "api_straincompass_import_failed")
		return
	}

	strain := mapStraincompassStrain(rec)

	strainID, err := upsertStraincompassStrain(db, breederID, strain)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to upsert strain")
		apiInternalError(c, "api_straincompass_import_failed")
		return
	}

	if err := replaceStraincompassAttributes(db, strainID, rec); err != nil {
		// Attribute data is best-effort; log but don't fail the import.
		fieldLogger.WithError(err).Warn("Failed to import strain attributes")
	}

	if _, err := seedLineageFromNote(db, strainID, rec.Lineage); err != nil {
		fieldLogger.WithError(err).Warn("Failed to record lineage from StrainCompass note")
	}

	// Refresh in-memory caches so the UI reflects the import immediately.
	store.SetBreeders(GetBreeders(db))
	store.SetStrains(GetStrains(db))

	fieldLogger.WithField("strain_id", strainID).WithField("slug", req.Slug).Info("Imported strain from StrainCompass")
	c.JSON(http.StatusOK, gin.H{
		"id":                strainID,
		"name":              strain.Name,
		"breeder_id":        breederID,
		"breeder":           breederName(db, breederID),
		"straincompass_url": StraincompassWebURL(rec.Slug),
		"message":           T(c, "api_straincompass_imported"),
	})
}

// StraincompassPreviewHandler returns the listing's own breeder so the
// import dialog can offer it as a one-click choice. The search endpoint the
// dialog uses doesn't carry breeders.
// GET /strains/straincompass/preview?slug=<slug>&name=<name>
func StraincompassPreviewHandler(c *gin.Context) {
	if !straincompassEnabled(c) {
		apiBadRequest(c, "api_straincompass_disabled")
		return
	}
	slug := strings.TrimSpace(c.Query("slug"))
	if slug == "" {
		apiBadRequest(c, "api_straincompass_invalid_slug")
		return
	}
	store := ConfigStoreFromContext(c)
	rec, err := straincompassGetStrainBySlug(store.StraincompassBaseURL(), store.StraincompassAPIKey(), slug, strings.TrimSpace(c.Query("name")))
	if err != nil {
		respondStraincompassError(c, err, "api_straincompass_search_failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": rec.Name, "breeder": strings.TrimSpace(rec.Breeder)})
}

// ---------------------------------------------------------------------------
// Mapping + persistence helpers
// ---------------------------------------------------------------------------

// mapStraincompassStrain converts a StrainCompass strain record into Isley's
// Strain.
func mapStraincompassStrain(rec *straincompassStrain) types.Strain {
	indica, sativa := normalizeIndicaSativa(rec.IndicaPercent, rec.SativaPercent)

	// floweringTimeMin/Max are reported in whole WEEKS (confirmed live:
	// e.g. 8-9 for a strain StrainCompass's own page describes as "8-9
	// weeks"), but Isley's cycle_time is in days (matching CannaDB's
	// mapping, which is genuinely day-denominated) — so convert. Prefer
	// max, fall back to min, same preference rule as CannaDB's mapping.
	const daysPerWeek = 7
	cycleTime := 0
	if rec.FloweringTimeMax != nil {
		cycleTime = *rec.FloweringTimeMax * daysPerWeek
	} else if rec.FloweringTimeMin != nil {
		cycleTime = *rec.FloweringTimeMin * daysPerWeek
	}

	verified := rec.Verified
	strain := types.Strain{
		Name:                     rec.Name,
		Indica:                   indica,
		Sativa:                   sativa,
		Description:              rec.Description, // imported directly, including any third-party copy
		ShortDescription:         rec.ShortDescription,
		CycleTime:                cycleTime,
		StraincompassSlug:        rec.Slug,
		StraincompassUpdatedAt:   rec.UpdatedAt,
		ThcMin:                   rec.ThcMin,
		ThcMax:                   rec.ThcMax,
		CbdMin:                   rec.CbdMin,
		CbdMax:                   rec.CbdMax,
		CbnMax:                   rec.CbnMax,
		CbgMax:                   rec.CbgMax,
		StraincompassVerified:    &verified,
		StraincompassQuality:     rec.QualityScore,
		StraincompassSources:     strings.Join(rec.Sources, ","),
		StraincompassLineageNote: rec.Lineage,
	}
	return strain
}

// normalizeIndicaSativa reconciles StrainCompass's independent
// indicaPercent/sativaPercent fields (which may not sum to 100) into
// Isley's required indica+sativa==100 pair: proportionally rescaled when
// both are present, the complement of whichever one is present, or a 50/50
// default when neither is present.
func normalizeIndicaSativa(indicaPercent, sativaPercent *int) (indica, sativa int) {
	switch {
	case indicaPercent != nil && sativaPercent != nil:
		i, s := *indicaPercent, *sativaPercent
		if i < 0 {
			i = 0
		}
		if s < 0 {
			s = 0
		}
		total := i + s
		if total == 0 {
			return 50, 50
		}
		sativa = int((float64(s) / float64(total)) * 100.0)
		if sativa < 0 {
			sativa = 0
		}
		if sativa > 100 {
			sativa = 100
		}
		return 100 - sativa, sativa
	case sativaPercent != nil:
		s := clampPercent(*sativaPercent)
		return 100 - s, s
	case indicaPercent != nil:
		i := clampPercent(*indicaPercent)
		return i, 100 - i
	default:
		return 50, 50
	}
}

func clampPercent(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// importStraincompassBreeder resolves the strain's breeder by name-dedupe
// only — StrainCompass's breeder field is a plain string, not a fetchable
// record, so there's no separate lookup call like CannaDB's.
func importStraincompassBreeder(db *sql.DB, name string) (int, error) {
	if strings.TrimSpace(name) == "" {
		name = "Unknown Breeder"
	}
	id, _, err := findOrCreateBreeder(db, name)
	return id, err
}

// upsertStraincompassStrain inserts a new strain or updates the existing one
// keyed on (straincompass_slug, breeder_id): the same listing bought from two
// breeders is two strains. Returns the strain id.
func upsertStraincompassStrain(db *sql.DB, breederID int, s types.Strain) (int, error) {
	verified := interface{}(nil)
	if s.StraincompassVerified != nil {
		if *s.StraincompassVerified {
			verified = 1
		} else {
			verified = 0
		}
	}

	var id int
	err := db.QueryRow("SELECT id FROM strain WHERE straincompass_slug = $1 AND breeder_id = $2", s.StraincompassSlug, breederID).Scan(&id)
	switch {
	case err == nil:
		_, uerr := db.Exec(`
			UPDATE strain
			SET name = $1, breeder_id = $2, indica = $3, sativa = $4,
			    description = $5, short_desc = $6, cycle_time = $7,
			    straincompass_updated_at = $8, thc_min = $9, thc_max = $10,
			    cbd_min = $11, cbd_max = $12, cbn_max = $13, cbg_max = $14,
			    straincompass_verified = $15, straincompass_quality_score = $16,
			    straincompass_sources = $17, straincompass_lineage_note = $18
			WHERE id = $19`,
			s.Name, breederID, s.Indica, s.Sativa,
			s.Description, s.ShortDescription, s.CycleTime,
			nullableStr(s.StraincompassUpdatedAt), s.ThcMin, s.ThcMax,
			s.CbdMin, s.CbdMax, s.CbnMax, s.CbgMax,
			verified, s.StraincompassQuality,
			nullableStr(s.StraincompassSources), nullableStr(s.StraincompassLineageNote), id)
		return id, uerr
	case errors.Is(err, sql.ErrNoRows):
		ierr := db.QueryRow(`
			INSERT INTO strain (name, breeder_id, indica, sativa, autoflower, seed_count,
			                    description, short_desc, cycle_time,
			                    straincompass_slug, straincompass_updated_at,
			                    thc_min, thc_max, cbd_min, cbd_max, cbn_max, cbg_max,
			                    straincompass_verified, straincompass_quality_score,
			                    straincompass_sources, straincompass_lineage_note)
			VALUES ($1, $2, $3, $4, 0, 0, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
			RETURNING id`,
			s.Name, breederID, s.Indica, s.Sativa,
			s.Description, s.ShortDescription, s.CycleTime,
			s.StraincompassSlug, nullableStr(s.StraincompassUpdatedAt),
			s.ThcMin, s.ThcMax, s.CbdMin, s.CbdMax, s.CbnMax, s.CbgMax,
			verified, s.StraincompassQuality,
			nullableStr(s.StraincompassSources), nullableStr(s.StraincompassLineageNote)).Scan(&id)
		return id, ierr
	default:
		return 0, err
	}
}

// replaceStraincompassAttributes rewrites the strain's effects/flavors/
// terpenes/medical-uses from the StrainCompass record. Only groups marked
// "sourced" (strain-specific, not generic category filler) in
// AttributeProvenance are written — "rule"-based groups never reach the
// database — and groups StrainCompass has no sourced data for are left as
// they are, so values the user entered by hand survive a re-import.
func replaceStraincompassAttributes(db *sql.DB, strainID int, rec *straincompassStrain) error {
	var set strainAttributeSet
	sourced := func(p string) bool { return p == straincompassProvenanceSourced }

	if sourced(rec.AttributeProvenance.Effects) {
		attrs := make([]types.StrainAttribute, 0, len(rec.Effects))
		for _, e := range rec.Effects {
			attrs = append(attrs, types.StrainAttribute{Name: e.Name, Intensity: e.Intensity})
		}
		set.Effects = &attrs
	}
	if sourced(rec.AttributeProvenance.Flavors) {
		attrs := make([]types.StrainAttribute, 0, len(rec.Flavors))
		for _, f := range rec.Flavors {
			attrs = append(attrs, types.StrainAttribute{Name: f.Name})
		}
		set.Flavors = &attrs
	}
	if sourced(rec.AttributeProvenance.Terpenes) {
		attrs := make([]types.StrainAttribute, 0, len(rec.Terpenes))
		for _, t := range rec.Terpenes {
			attrs = append(attrs, types.StrainAttribute{Name: t.Name, Level: t.Level})
		}
		set.Terpenes = &attrs
	}
	if sourced(rec.AttributeProvenance.Medical) {
		attrs := make([]types.StrainAttribute, 0, len(rec.MedicalUses))
		for _, m := range rec.MedicalUses {
			attrs = append(attrs, types.StrainAttribute{Name: m.Name})
		}
		set.MedicalUses = &attrs
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := replaceStrainAttributes(tx, strainID, set); err != nil {
		return err
	}
	return tx.Commit()
}

// respondStraincompassError maps a client error to an appropriate API
// response.
func respondStraincompassError(c *gin.Context, err error, fallbackKey string) {
	if errors.Is(err, errStraincompassNotFound) {
		apiNotFound(c, "api_straincompass_not_found")
		return
	}
	var apiErr *straincompassError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatus {
		case http.StatusNotFound:
			apiNotFound(c, "api_straincompass_not_found")
			return
		case http.StatusTooManyRequests:
			c.JSON(http.StatusTooManyRequests, gin.H{"error": T(c, "api_straincompass_rate_limited")})
			return
		case http.StatusUnauthorized, http.StatusForbidden:
			apiBadRequest(c, "api_straincompass_invalid_key")
			return
		}
	}
	logger.Log.WithError(err).Error("StrainCompass request failed")
	apiInternalError(c, fallbackKey)
}
