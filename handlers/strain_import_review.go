package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"isley/logger"
	"isley/model/types"
	"isley/utils"

	"github.com/gin-gonic/gin"
)

// Review-before-save imports. Choosing a StrainCompass or CannaDB listing
// opens a "Review Strain Info" page pre-filled with everything that would be
// imported; nothing is written until the user saves it.

const (
	importSourceStraincompass = "straincompass"
	importSourceCannadb       = "cannadb"
	maxImportParents          = 20
	maxImportKeyLength        = 1000
)

var (
	errImportSourceDisabled = errors.New("import source disabled")
	errImportBadRequest     = errors.New("bad import request")
)

// importProvenance says where a draft came from; it rides along on the page
// and is saved with the strain.
type importProvenance struct {
	Source           string   `json:"source"` // importSourceStraincompass or importSourceCannadb
	Key              string   `json:"key"`    // StrainCompass slug or CannaDB AT-URI
	UpdatedAt        string   `json:"updated_at,omitempty"`
	Verified         *bool    `json:"verified,omitempty"`
	Quality          *float64 `json:"quality,omitempty"`
	Sources          string   `json:"sources,omitempty"`
	LineageNote      string   `json:"lineage_note,omitempty"`
	LineageSource    string   `json:"lineage_source,omitempty"`
	LineageSourceURI string   `json:"lineage_source_uri,omitempty"`
}

type importParent struct {
	Name     string `json:"parent_name"`
	StrainID *int   `json:"parent_strain_id"`
}

// importReviewDraft is the part of the review page the browser needs back
// when saving.
type importReviewDraft struct {
	Parents        []importParent   `json:"parents"`
	Provenance     importProvenance `json:"provenance"`
	PackagingToken string           `json:"packaging_token,omitempty"`
}

// ImportReview is everything the "Review Strain Info" page renders.
type ImportReview struct {
	Strain          types.Strain
	Error           string // translated; the page shows only this and a way back
	SourceName      string
	SourceURL       string
	ExistingID      int
	ExistingBreeder string
	PackagingURL    string // CannaDB seed-pack image offered for this draft
	Draft           importReviewDraft
}

// PrepareImportReview builds the review page for the listing named in the
// query string (source=straincompass&slug=…&name=…&listing_breeder=…, or
// source=cannadb&uri=…). It reads from the sources but writes nothing
// except a held copy of an offered seed-pack image.
func PrepareImportReview(c *gin.Context) ImportReview {
	source := c.Query("source")
	review, err := buildImportReview(c.Request.Context(), DBFromContext(c), ConfigStoreFromContext(c).CannadbEnabled() == 1,
		importSourceSettings(c, source), UploadDirFromContext(c), source, c.Request.URL.Query())
	if err != nil {
		logger.Log.WithField("func", "PrepareImportReview").WithError(err).Warn("Could not build import review")
		review.Error = T(c, importErrorKey(source, err))
	}
	return review
}

type importSettings struct {
	enabled bool
	baseURL string
	apiKey  string
	cannadb string // CannaDB base URL, for StrainCompass gap-fill
}

func importSourceSettings(c *gin.Context, source string) importSettings {
	store := ConfigStoreFromContext(c)
	s := importSettings{cannadb: store.CannadbBaseURL()}
	switch source {
	case importSourceStraincompass:
		s.enabled = store.StraincompassEnabled() == 1
		s.baseURL, s.apiKey = store.StraincompassBaseURL(), store.StraincompassAPIKey()
	case importSourceCannadb:
		s.enabled = store.CannadbEnabled() == 1
		s.baseURL = store.CannadbBaseURL()
	}
	return s
}

func importErrorKey(source string, err error) string {
	prefix := "api_straincompass_"
	if source == importSourceCannadb {
		prefix = "api_cannadb_"
	}
	switch {
	case errors.Is(err, errImportSourceDisabled):
		return prefix + "disabled"
	case errors.Is(err, errImportBadRequest):
		return "api_invalid_request"
	case errors.Is(err, errStraincompassNotFound):
		return "api_straincompass_not_found"
	default:
		return prefix + "import_failed"
	}
}

func buildImportReview(ctx context.Context, db *sql.DB, cannadbOn bool, s importSettings, uploadDir, source string, q url.Values) (ImportReview, error) {
	var r ImportReview
	if !s.enabled {
		return r, errImportSourceDisabled
	}

	var parentNames []string
	var imgRec *cannadbRecord
	var imgVal *cannadbStrainValue

	switch source {
	case importSourceStraincompass:
		r.SourceName = "StrainCompass"
		slug := strings.TrimSpace(q.Get("slug"))
		if slug == "" {
			return r, errImportBadRequest
		}
		rec, err := straincompassGetStrainBySlug(s.baseURL, s.apiKey, slug, q.Get("name"), q.Get("listing_breeder"))
		if err != nil {
			return r, err
		}
		r.Strain = mapStraincompassStrain(rec)
		r.Strain.Breeder = strings.TrimSpace(rec.Breeder)
		set := straincompassAttributeSet(rec)
		r.Strain.Effects, r.Strain.Flavors = derefAttrs(set.Effects), derefAttrs(set.Flavors)
		r.Strain.Terpenes, r.Strain.MedicalUses = derefAttrs(set.Terpenes), derefAttrs(set.MedicalUses)
		r.SourceURL = StraincompassWebURL(rec.Slug)
		r.Draft.Provenance = importProvenance{
			Source: importSourceStraincompass, Key: rec.Slug, UpdatedAt: rec.UpdatedAt,
			Verified: r.Strain.StraincompassVerified, Quality: rec.QualityScore,
			Sources: r.Strain.StraincompassSources, LineageNote: rec.Lineage,
		}
		if parsed, ok := parseLineageNote(rec.Lineage); ok {
			parentNames = parsed
			r.Draft.Provenance.LineageSource = lineageSourceStraincompass
		} else if cannadbOn {
			// Same gap-fill as a one-click import: an exact-name CannaDB match.
			if rows, err := cannadbSearchStrains(s.cannadb, r.Strain.Name, 10); err == nil {
				if m := findCannadbMatch(rows, r.Strain.Name, r.Strain.Breeder); m != nil {
					if cRec, cVal, err := cannadbGetStrain(s.cannadb, m.URI); err == nil {
						parentNames = repairCannadbParentNames(cVal.ParentNames)
						if len(parentNames) > 0 {
							r.Draft.Provenance.LineageSource = lineageSourceCannadb
							r.Draft.Provenance.LineageSourceURI = cRec.URI
						}
						imgRec, imgVal = cRec, cVal
					}
				}
			}
		}

	case importSourceCannadb:
		r.SourceName = "CannaDB"
		uri := strings.TrimSpace(q.Get("uri"))
		if !strings.HasPrefix(uri, "at://") {
			return r, errImportBadRequest
		}
		rec, val, err := cannadbGetStrain(s.baseURL, uri)
		if err != nil {
			return r, err
		}
		r.Strain = mapCannadbStrain(rec, val)
		r.Strain.Breeder = strings.TrimSpace(val.BreederName)
		if r.Strain.Breeder == "" && val.Breeder != "" {
			if _, bVal, err := cannadbGetBreeder(s.baseURL, val.Breeder); err == nil {
				r.Strain.Breeder = strings.TrimSpace(bVal.Name)
			}
		}
		r.SourceURL = cannadbWebURL(rec.URI)
		r.Draft.Provenance = importProvenance{Source: importSourceCannadb, Key: rec.URI, UpdatedAt: rec.IndexedAt}
		parentNames = repairCannadbParentNames(val.ParentNames)
		if len(parentNames) > 0 {
			r.Draft.Provenance.LineageSource = lineageSourceCannadb
			r.Draft.Provenance.LineageSourceURI = rec.URI
		}
		imgRec, imgVal = rec, val

	default:
		return r, errImportBadRequest
	}

	r.ExistingID = findExistingImport(db, r.Draft.Provenance, r.Strain.Breeder)
	if r.ExistingID > 0 {
		parentNames = mergeWithExisting(db, &r, parentNames)
	}
	r.Draft.Parents = linkParents(db, parentNames, r.ExistingID)

	if r.Strain.PackagingImage == "" {
		if body, ext, ok := downloadCannadbPackagingImage(ctx, imgRec, imgVal); ok {
			if token, url, err := stageDraftPackagingImage(uploadDir, body, ext); err == nil {
				r.Draft.PackagingToken, r.PackagingURL = token, url
			}
		}
	}
	return r, nil
}

func derefAttrs(p *[]types.StrainAttribute) []types.StrainAttribute {
	if p == nil {
		return nil
	}
	return *p
}

// findExistingImport returns the id of the strain already imported from
// this listing for this breeder (0 when none).
func findExistingImport(db *sql.DB, prov importProvenance, breeder string) int {
	col := importKeyColumn(prov.Source)
	if col == "" || prov.Key == "" || strings.TrimSpace(breeder) == "" {
		return 0
	}
	var id int
	err := db.QueryRow(`SELECT s.id FROM strain s JOIN breeder b ON b.id = s.breeder_id
		WHERE s.`+col+` = $1 AND LOWER(b.name) = LOWER($2) ORDER BY s.id LIMIT 1`, prov.Key, strings.TrimSpace(breeder)).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}

func importKeyColumn(source string) string {
	switch source {
	case importSourceStraincompass:
		return "straincompass_slug"
	case importSourceCannadb:
		return "cannadb_uri"
	}
	return ""
}

// mergeWithExisting starts the draft from the source's data but keeps what
// the user owns on the strain they already have: seed count, seed location,
// seed-pack image, anything the source lacks, and parents they entered
// themselves. Returns the parent names to show.
func mergeWithExisting(db *sql.DB, r *ImportReview, sourceParents []string) []string {
	ex := GetStrain(db, strconv.Itoa(r.ExistingID))
	if ex.ID == 0 {
		r.ExistingID = 0
		return sourceParents
	}
	s := &r.Strain
	r.ExistingBreeder = ex.Breeder
	s.ID = ex.ID
	s.SeedCount, s.SeedLocation, s.PackagingImage = ex.SeedCount, ex.SeedLocation, ex.PackagingImage
	if r.Draft.Provenance.Source == importSourceStraincompass {
		// StrainCompass doesn't report these.
		s.Autoflower = ex.Autoflower
	}
	if s.Url == "" {
		s.Url = ex.Url
	}
	if s.Description == "" {
		s.Description = ex.Description
	}
	if s.ShortDescription == "" {
		s.ShortDescription = ex.ShortDescription
	}
	if s.CycleTime == 0 {
		s.CycleTime = ex.CycleTime
	}
	for _, p := range []struct{ dst, src **float64 }{
		{&s.ThcMin, &ex.ThcMin}, {&s.ThcMax, &ex.ThcMax}, {&s.CbdMin, &ex.CbdMin},
		{&s.CbdMax, &ex.CbdMax}, {&s.CbnMax, &ex.CbnMax}, {&s.CbgMax, &ex.CbgMax},
	} {
		if *p.dst == nil {
			*p.dst = *p.src
		}
	}
	for _, p := range []struct{ dst, src *string }{
		{&s.HeightIndoor, &ex.HeightIndoor}, {&s.HeightOutdoor, &ex.HeightOutdoor},
		{&s.YieldIndoor, &ex.YieldIndoor}, {&s.YieldOutdoor, &ex.YieldOutdoor},
	} {
		if *p.dst == "" {
			*p.dst = *p.src
		}
	}
	for _, g := range []struct{ dst, src *[]types.StrainAttribute }{
		{&s.Effects, &ex.Effects}, {&s.Flavors, &ex.Flavors},
		{&s.Terpenes, &ex.Terpenes}, {&s.MedicalUses, &ex.MedicalUses},
	} {
		if len(*g.dst) == 0 {
			*g.dst = *g.src
		}
	}

	existing := GetLineage(db, ex.ID)
	userOwned := len(existing) > 0 && ex.LineageSource == ""
	if userOwned || (len(sourceParents) == 0 && len(existing) > 0) {
		names := make([]string, 0, len(existing))
		for _, l := range existing {
			names = append(names, l.ParentName)
		}
		r.Draft.Provenance.LineageSource, r.Draft.Provenance.LineageSourceURI = ex.LineageSource, ex.LineageSourceURI
		return names
	}
	return sourceParents
}

// linkParents pairs each parent name with the one local strain of that name,
// when there is exactly one.
func linkParents(db *sql.DB, names []string, excludeID int) []importParent {
	parents := make([]importParent, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		key := strings.ToLower(n)
		if n == "" || seen[key] || len(n) > utils.MaxNameLength {
			continue
		}
		seen[key] = true
		p := importParent{Name: n}
		if id, err := uniqueLocalStrainID(db, n, excludeID); err == nil && id != nil {
			v := id.(int)
			p.StrainID = &v
		}
		parents = append(parents, p)
	}
	return parents
}

// SaveImportedStrainHandler saves a reviewed import: it updates the strain
// already imported from that listing for the chosen breeder, or adds a new
// one. POST /strains/import/save
func SaveImportedStrainHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "SaveImportedStrainHandler")

	var req struct {
		Name             string              `json:"name"`
		BreederID        *int                `json:"breeder_id"`
		NewBreeder       string              `json:"new_breeder"`
		Indica           int                 `json:"indica"`
		Sativa           int                 `json:"sativa"`
		Autoflower       bool                `json:"autoflower"`
		Description      string              `json:"description"`
		ShortDescription string              `json:"short_desc"`
		SeedCount        int                 `json:"seed_count"`
		SeedLocation     string              `json:"seed_location"`
		CycleTime        int                 `json:"cycle_time"`
		Url              string              `json:"url"`
		Growing          *strainGrowingInfo  `json:"growing"`
		Cannabinoids     *strainCannabinoids `json:"cannabinoids"`
		Attributes       *struct {
			Effects     []types.StrainAttribute `json:"effects"`
			Flavors     []types.StrainAttribute `json:"flavors"`
			Terpenes    []types.StrainAttribute `json:"terpenes"`
			MedicalUses []types.StrainAttribute `json:"medical_uses"`
		} `json:"attributes"`
		Parents           []importParent   `json:"parents"`
		Provenance        importProvenance `json:"provenance"`
		KeepLineageSource bool             `json:"keep_lineage_source"`
		Packaging         struct {
			Choice string `json:"choice"` // keep | none | upload
			Token  string `json:"token"`
		} `json:"packaging"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiBadRequest(c, "api_invalid_request_payload")
		return
	}

	prov := req.Provenance
	keyCol := importKeyColumn(prov.Source)
	prov.Key = strings.TrimSpace(prov.Key)
	if keyCol == "" || prov.Key == "" || len(prov.Key) > maxImportKeyLength ||
		(prov.Source == importSourceCannadb && !strings.HasPrefix(prov.Key, "at://")) {
		apiBadRequest(c, "api_invalid_request")
		return
	}
	for _, f := range []string{prov.UpdatedAt, prov.Sources, prov.LineageSourceURI} {
		if len(f) > maxImportKeyLength {
			apiBadRequest(c, "api_invalid_request")
			return
		}
	}
	if len(prov.LineageNote) > utils.MaxDescriptionLength {
		apiBadRequest(c, "api_invalid_request")
		return
	}

	req.Url = utils.NormalizeWebURL(req.Url)
	if err := validateStrainFields(req.Name, req.Description, req.ShortDescription, req.NewBreeder, req.Url); err != nil {
		apiBadRequest(c, err.Error())
		return
	}
	if req.Indica+req.Sativa != 100 {
		apiBadRequest(c, "api_indica_sativa_must_sum_100")
		return
	}
	req.SeedLocation = strings.TrimSpace(req.SeedLocation)
	if err := utils.ValidateStringLength("seed_location", req.SeedLocation, utils.MaxNameLength); err != nil {
		apiBadRequest(c, err.Error())
		return
	}
	var growing strainGrowingInfo
	if req.Growing != nil {
		growing = *req.Growing
		if err := growing.normalize(); err != nil {
			apiBadRequest(c, err.Error())
			return
		}
	}
	var cb strainCannabinoids
	if req.Cannabinoids != nil {
		cb = *req.Cannabinoids
		if key := cb.validate(); key != "" {
			apiBadRequest(c, key)
			return
		}
	}
	var attrs strainAttributeSet
	if a := req.Attributes; a != nil {
		for _, g := range []struct {
			field string
			list  []types.StrainAttribute
		}{{"effect", a.Effects}, {"flavor", a.Flavors}, {"terpene", a.Terpenes}, {"medical_use", a.MedicalUses}} {
			if err := validateStrainAttributeGroup(g.field, g.list); err != nil {
				apiBadRequest(c, err.Error())
				return
			}
		}
		attrs = strainAttributeSet{Effects: &a.Effects, Flavors: &a.Flavors, Terpenes: &a.Terpenes, MedicalUses: &a.MedicalUses}
	} else {
		empty := []types.StrainAttribute{}
		attrs = strainAttributeSet{Effects: &empty, Flavors: &empty, Terpenes: &empty, MedicalUses: &empty}
	}
	if len(req.Parents) > maxImportParents {
		apiBadRequest(c, "api_invalid_request")
		return
	}
	for _, p := range req.Parents {
		if err := utils.ValidateStringLength("parent_name", strings.TrimSpace(p.Name), utils.MaxNameLength); err != nil {
			apiBadRequest(c, err.Error())
			return
		}
	}

	db := DBFromContext(c)
	breederID, ok := resolveRequestBreeder(c, db, req.BreederID, req.NewBreeder)
	if !ok {
		return
	}

	lineageSource, lineageURI := "", ""
	if req.KeepLineageSource && (prov.LineageSource == lineageSourceStraincompass || prov.LineageSource == lineageSourceCannadb) {
		lineageSource, lineageURI = prov.LineageSource, prov.LineageSourceURI
	}

	autoflower := 0
	if req.Autoflower {
		autoflower = 1
	}
	cols := []string{"name", "breeder_id", "indica", "sativa", "autoflower", "seed_count", "seed_location",
		"description", "short_desc", "cycle_time", "url",
		"height_indoor", "height_outdoor", "yield_indoor", "yield_outdoor",
		"thc_min", "thc_max", "cbd_min", "cbd_max", "cbn_max", "cbg_max",
		"lineage_source", "lineage_source_uri", keyCol}
	vals := []any{req.Name, breederID, req.Indica, req.Sativa, autoflower, req.SeedCount, nullableStr(req.SeedLocation),
		req.Description, req.ShortDescription, req.CycleTime, req.Url,
		nullableStr(growing.HeightIndoor), nullableStr(growing.HeightOutdoor), nullableStr(growing.YieldIndoor), nullableStr(growing.YieldOutdoor),
		cb.ThcMin, cb.ThcMax, cb.CbdMin, cb.CbdMax, cb.CbnMax, cb.CbgMax,
		nullableStr(lineageSource), nullableStr(lineageURI), prov.Key}
	switch prov.Source {
	case importSourceStraincompass:
		var verified any
		if prov.Verified != nil {
			verified = 0
			if *prov.Verified {
				verified = 1
			}
		}
		cols = append(cols, "straincompass_updated_at", "straincompass_verified", "straincompass_quality_score",
			"straincompass_sources", "straincompass_lineage_note")
		vals = append(vals, nullableStr(prov.UpdatedAt), verified, prov.Quality, nullableStr(prov.Sources), nullableStr(prov.LineageNote))
	case importSourceCannadb:
		cols = append(cols, "cannadb_indexed_at")
		vals = append(vals, nullableStr(prov.UpdatedAt))
	}

	tx, err := db.Begin()
	if err != nil {
		apiInternalError(c, "api_failed_to_add_strain")
		return
	}
	defer func() { _ = tx.Rollback() }()

	var strainID int
	err = tx.QueryRow("SELECT id FROM strain WHERE "+keyCol+" = $1 AND breeder_id = $2", prov.Key, breederID).Scan(&strainID)
	updated := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fieldLogger.WithError(err).Error("Failed to look up existing strain")
		apiInternalError(c, "api_failed_to_add_strain")
		return
	}
	if updated {
		err = execUpdateStrain(tx, strainID, cols, vals)
	} else {
		strainID, err = execInsertStrain(tx, cols, vals)
	}
	if err == nil {
		err = replaceStrainAttributes(tx, strainID, attrs)
	}
	if err == nil {
		err = replaceStrainLineageTx(tx, strainID, req.Parents)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to save imported strain")
		apiInternalError(c, "api_failed_to_add_strain")
		return
	}

	uploadDir := UploadDirFromContext(c)
	switch req.Packaging.Choice {
	case "keep":
		if err := adoptDraftPackagingImage(db, uploadDir, req.Packaging.Token, strainID); err != nil {
			fieldLogger.WithError(err).Warn("Could not keep the seed-pack image")
		}
	default:
		removeDraftPackaging(uploadDir, req.Packaging.Token)
	}

	ConfigStoreFromContext(c).SetStrains(GetStrains(db))
	msg := "api_import_saved"
	if updated {
		msg = "api_import_updated"
	}
	c.JSON(http.StatusOK, gin.H{"id": strainID, "updated": updated, "message": T(c, msg)})
}

func execInsertStrain(tx *sql.Tx, cols []string, vals []any) (int, error) {
	marks := make([]string, len(cols))
	for i := range cols {
		marks[i] = "$" + strconv.Itoa(i+1)
	}
	var id int
	err := tx.QueryRow("INSERT INTO strain ("+strings.Join(cols, ", ")+") VALUES ("+strings.Join(marks, ", ")+") RETURNING id", vals...).Scan(&id)
	return id, err
}

func execUpdateStrain(tx *sql.Tx, id int, cols []string, vals []any) error {
	sets := make([]string, len(cols))
	for i, c := range cols {
		sets[i] = c + " = $" + strconv.Itoa(i+1)
	}
	_, err := tx.Exec("UPDATE strain SET "+strings.Join(sets, ", ")+" WHERE id = $"+strconv.Itoa(len(cols)+1), append(vals, id)...)
	return err
}

// replaceStrainLineageTx rewrites a strain's parents. Links to strains that
// don't exist (or to the strain itself) are dropped, leaving the name.
func replaceStrainLineageTx(tx *sql.Tx, strainID int, parents []importParent) error {
	if _, err := tx.Exec("DELETE FROM strain_lineage WHERE strain_id = $1", strainID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range parents {
		name := strings.TrimSpace(p.Name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		var link any
		if p.StrainID != nil && *p.StrainID != strainID {
			var n int
			if err := tx.QueryRow("SELECT COUNT(*) FROM strain WHERE id = $1", *p.StrainID).Scan(&n); err != nil {
				return err
			}
			if n == 1 {
				link = *p.StrainID
			}
		}
		if _, err := tx.Exec("INSERT INTO strain_lineage (strain_id, parent_name, parent_strain_id) VALUES ($1, $2, $3)",
			strainID, name, link); err != nil {
			return err
		}
	}
	return nil
}
