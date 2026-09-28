package handlers

import (
	"database/sql"
	"errors"
	"isley/logger"
	model "isley/model"
	"isley/model/types"
	"isley/utils"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Breeder helpers & handlers
// ---------------------------------------------------------------------------

// rowQuerier is satisfied by both *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

var errBreederNameRequired = errors.New("breeder name is required")

// findOrCreateBreeder returns the id of the breeder whose name matches name
// case-insensitively (after trimming), inserting it if none exists, so
// "Seed Junky" typed twice never yields two breeder rows. created reports
// whether a row was inserted. LOWER() folds ASCII only on SQLite.
func findOrCreateBreeder(q rowQuerier, name string) (id int, created bool, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false, errBreederNameRequired
	}
	err = q.QueryRow("SELECT id FROM breeder WHERE LOWER(name) = LOWER($1) ORDER BY id LIMIT 1", name).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if err = q.QueryRow("INSERT INTO breeder (name) VALUES ($1) RETURNING id", name).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

var errImportBreederNotFound = errors.New("chosen breeder does not exist")

// resolveImportBreeder picks the breeder for an imported strain: the breeder
// the user chose (by id or by name), else fallback — the source's own
// breeder, kept for API callers that don't choose one.
func resolveImportBreeder(db *sql.DB, breederID *int, newBreeder string, fallback func() (int, error)) (int, error) {
	if breederID != nil {
		var id int
		err := db.QueryRow("SELECT id FROM breeder WHERE id = $1", *breederID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errImportBreederNotFound
		}
		return id, err
	}
	if strings.TrimSpace(newBreeder) != "" {
		id, _, err := findOrCreateBreeder(db, newBreeder)
		return id, err
	}
	return fallback()
}

// breederName returns the breeder's name, or "" if it can't be read.
func breederName(db *sql.DB, id int) string {
	var name string
	_ = db.QueryRow("SELECT name FROM breeder WHERE id = $1", id).Scan(&name)
	return name
}

// resolveRequestBreeder turns a request's breeder_id / new_breeder pair into
// a breeder id, writing the API error response itself when it can't.
func resolveRequestBreeder(c *gin.Context, db *sql.DB, breederID *int, newBreeder string) (int, bool) {
	if breederID != nil {
		return *breederID, true
	}
	id, created, err := findOrCreateBreeder(db, newBreeder)
	if errors.Is(err, errBreederNameRequired) {
		apiBadRequest(c, "api_new_breeder_name_required")
		return 0, false
	}
	if err != nil {
		logger.Log.WithField("func", "resolveRequestBreeder").WithError(err).Error("Failed to add new breeder")
		apiInternalError(c, "api_failed_to_add_new_breeder")
		return 0, false
	}
	if created {
		ConfigStoreFromContext(c).SetBreeders(GetBreeders(db))
	}
	return id, true
}

func GetBreeders(db *sql.DB) []types.Breeder {
	fieldLogger := logger.Log.WithField("func", "GetBreeders")

	rows, err := db.Query("SELECT id, name FROM breeder ORDER BY LOWER(name), id")
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to query breeders")
		return nil
	}

	var breeders []types.Breeder
	for rows.Next() {
		var breeder types.Breeder
		err = rows.Scan(&breeder.ID, &breeder.Name)
		if err != nil {
			fieldLogger.WithError(err).Error("Failed to scan breeder")
			return nil
		}
		breeders = append(breeders, breeder)
	}

	return breeders
}

func AddBreederHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "AddBreederHandler")
	var breeder struct {
		Name string `json:"breeder_name"`
	}
	if err := c.ShouldBindJSON(&breeder); err != nil {
		fieldLogger.WithError(err).Error("Failed to add breeder")
		apiBadRequest(c, "api_invalid_payload")
		return
	}
	if err := utils.ValidateRequiredString("breeder_name", breeder.Name, utils.MaxNameLength); err != nil {
		apiBadRequest(c, err.Error())
		return
	}

	db := DBFromContext(c)
	id, created, err := findOrCreateBreeder(db, breeder.Name)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to add breeder")
		apiInternalError(c, "api_failed_to_add_breeder")
		return
	}
	if !created {
		c.JSON(http.StatusOK, gin.H{"id": id, "existing": true})
		return
	}
	ConfigStoreFromContext(c).SetBreeders(GetBreeders(db))

	c.JSON(http.StatusCreated, gin.H{"id": id})
}
func UpdateBreederHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "UpdateBreederHandler")
	id := c.Param("id")
	var breeder struct {
		Name string `json:"breeder_name"`
	}
	if err := c.ShouldBindJSON(&breeder); err != nil {
		fieldLogger.WithError(err).Error("Failed to update breeder")
		apiBadRequest(c, "api_invalid_payload")
		return
	}
	if err := utils.ValidateRequiredString("breeder_name", breeder.Name, utils.MaxNameLength); err != nil {
		apiBadRequest(c, err.Error())
		return
	}

	// Update breeder in database
	db := DBFromContext(c)

	// Update breeder in database
	_, err := db.Exec("UPDATE breeder SET name = $1 WHERE id = $2", breeder.Name, id)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to update breeder")
		apiInternalError(c, "api_failed_to_update_breeder")
		return
	}

	//Reload Config
	ConfigStoreFromContext(c).SetBreeders(GetBreeders(db))

	apiOK(c, "api_breeder_updated")
}

func DeleteBreederHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "DeleteBreederHandler")
	id := c.Param("id")

	// Delete breeder from database
	db := DBFromContext(c)

	// Delete any plants associated with this breeder
	rows, err := db.Query("SELECT p.id FROM plant p LEFT OUTER JOIN strain s on s.id = p.strain_id WHERE s.breeder_id = $1", id)
	if err != nil {
		if err.Error() != "sql: no rows in result set" {

		} else {
			fieldLogger.WithError(err).Error("Failed to delete plants")
			return
		}
	}
	defer rows.Close()

	plantList := []int{}
	for rows.Next() {
		var plantId int
		err = rows.Scan(&plantId)
		if err != nil {
			fieldLogger.WithError(err).Error("Failed to delete plant")
			continue
		}
		plantList = append(plantList, plantId)
	}

	for _, plantId := range plantList {
		DeletePlantById(db, strconv.Itoa(plantId))
	}

	// Delete any strains associated with this breeder
	_, err = db.Exec("DELETE FROM strain WHERE breeder_id = $1", id)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to delete strains")
		apiInternalError(c, "api_failed_to_delete_strains")
	}

	// Delete breeder from database
	_, err = db.Exec("DELETE FROM breeder WHERE id = $1", id)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to delete breeder")
		apiInternalError(c, "api_failed_to_delete_breeder")
		return
	}

	//Reload Config
	ConfigStoreFromContext(c).SetBreeders(GetBreeders(db))

	apiOK(c, "api_breeder_deleted")
}

// ---------------------------------------------------------------------------
// Strain helpers & handlers
// ---------------------------------------------------------------------------

func validateStrainFields(name, description, shortDesc, newBreeder, strainURL string) error {
	if err := utils.ValidateRequiredString("name", name, utils.MaxNameLength); err != nil {
		return err
	}
	// description / short_desc are unbounded TEXT columns and were never
	// length-checked before v0.2.0. Use generous caps so legacy strains with
	// long free text still save (only pathological input is rejected).
	if err := utils.ValidateStringLength("description", description, utils.MaxNotesLength); err != nil {
		return err
	}
	if err := utils.ValidateStringLength("short_desc", shortDesc, utils.MaxDescriptionLength); err != nil {
		return err
	}
	if err := utils.ValidateStringLength("new_breeder", newBreeder, utils.MaxNameLength); err != nil {
		return err
	}
	return utils.ValidateWebURL("url", strainURL)
}

func GetStrains(db *sql.DB) []types.Strain {
	fieldLogger := logger.Log.WithField("func", "GetStrains")

	// breeder_id/breeder are coalesced because a strain with no matching
	// breeder row (e.g. breeder_id 0, or a breeder deleted out from under
	// it) otherwise LEFT JOINs to NULL, which fails to scan into the
	// non-nullable int/string fields below and previously blanked the
	// entire strain list rather than just that one row.
	rows, err := db.Query("SELECT s.id, s.name, coalesce(b.id, 0) as breeder_id, coalesce(b.name, '') as breeder, s.indica, s.sativa, s.autoflower, s.description, coalesce(s.short_desc, ''), s.seed_count, s.thc_min, s.thc_max FROM strain s left outer join breeder b on s.breeder_id = b.id ORDER BY s.name ASC")
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to query strains")
		return nil
	}

	var strains []types.Strain
	for rows.Next() {
		var strain types.Strain
		err = rows.Scan(&strain.ID, &strain.Name, &strain.BreederID, &strain.Breeder, &strain.Indica, &strain.Sativa, &strain.Autoflower, &strain.Description, &strain.ShortDescription, &strain.SeedCount, &strain.ThcMin, &strain.ThcMax)
		if err != nil {
			fieldLogger.WithError(err).Error("Failed to scan strain")
			return nil
		}
		strains = append(strains, strain)
	}

	return strains
}

func GetStrain(db *sql.DB, id string) types.Strain {
	fieldLogger := logger.Log.WithField("func", "GetStrain")

	var strain types.Strain
	var straincompassVerified sql.NullBool
	//join in breeder name
	err := db.QueryRow(`
		SELECT s.id, s.name, coalesce(s.short_desc, ''), b.name AS breeder, b.id as breeder_id, s.indica, s.sativa, s.autoflower, s.seed_count, s.description, coalesce(s.cycle_time, 0), coalesce(s.url, ''), coalesce(s.cannadb_uri, ''),
		       coalesce(s.straincompass_slug, ''), coalesce(s.straincompass_updated_at, ''), s.thc_min, s.thc_max, s.cbd_min, s.cbd_max, s.cbn_max, s.cbg_max,
		       s.straincompass_verified, s.straincompass_quality_score, coalesce(s.straincompass_sources, ''), coalesce(s.straincompass_lineage_note, '')
		FROM strain s
		JOIN breeder b ON s.breeder_id = b.id
		WHERE s.id = $1`, id).Scan(
		&strain.ID, &strain.Name, &strain.ShortDescription, &strain.Breeder, &strain.BreederID, &strain.Indica, &strain.Sativa, &strain.Autoflower, &strain.SeedCount, &strain.Description, &strain.CycleTime, &strain.Url, &strain.CannadbURI,
		&strain.StraincompassSlug, &strain.StraincompassUpdatedAt, &strain.ThcMin, &strain.ThcMax, &strain.CbdMin, &strain.CbdMax, &strain.CbnMax, &strain.CbgMax,
		&straincompassVerified, &strain.StraincompassQuality, &strain.StraincompassSources, &strain.StraincompassLineageNote)
	if err != nil {
		if err == sql.ErrNoRows {
			fieldLogger.Error("Strain not found")
		} else {
			fieldLogger.WithError(err).Error("Failed to fetch strain")
		}
		return types.Strain{}
	}
	if straincompassVerified.Valid {
		strain.StraincompassVerified = &straincompassVerified.Bool
	}

	strain.Effects = getStrainAttributes(db, "strain_effect", strain.ID, true, false)
	strain.Flavors = getStrainAttributes(db, "strain_flavor", strain.ID, false, false)
	strain.Terpenes = getStrainAttributes(db, "strain_terpene", strain.ID, false, true)
	strain.MedicalUses = getStrainAttributes(db, "strain_medical_use", strain.ID, false, false)

	return strain
}

// getStrainAttributes fetches rows from one of the strain attribute child
// tables (strain_effect/flavor/terpene/medical_use) for the strain detail
// view. withIntensity/withLevel select the table's optional numeric column
// (effects carry intensity, terpenes carry level; flavors/medical uses have
// neither). table is always one of the four constant table names above —
// never user input — so building the query string is safe here.
func getStrainAttributes(db *sql.DB, table string, strainID int, withIntensity, withLevel bool) []types.StrainAttribute {
	fieldLogger := logger.Log.WithField("func", "getStrainAttributes").WithField("table", table)

	col := "NULL"
	switch {
	case withIntensity:
		col = "intensity"
	case withLevel:
		col = "level"
	}

	rows, err := db.Query("SELECT id, name, "+col+" FROM "+table+" WHERE strain_id = $1 ORDER BY name ASC", strainID)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to query strain attributes")
		return nil
	}
	defer rows.Close()

	var attrs []types.StrainAttribute
	for rows.Next() {
		var attr types.StrainAttribute
		if withIntensity {
			var val sql.NullFloat64
			if err := rows.Scan(&attr.ID, &attr.Name, &val); err != nil {
				fieldLogger.WithError(err).Error("Failed to scan strain attribute")
				return nil
			}
			if val.Valid {
				attr.Intensity = &val.Float64
			}
		} else if withLevel {
			var val sql.NullString
			if err := rows.Scan(&attr.ID, &attr.Name, &val); err != nil {
				fieldLogger.WithError(err).Error("Failed to scan strain attribute")
				return nil
			}
			if val.Valid {
				attr.Level = &val.String
			}
		} else {
			var unused sql.NullString
			if err := rows.Scan(&attr.ID, &attr.Name, &unused); err != nil {
				fieldLogger.WithError(err).Error("Failed to scan strain attribute")
				return nil
			}
		}
		attrs = append(attrs, attr)
	}

	return attrs
}

func AddStrainHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "AddStrainHandler")
	// Parse the incoming JSON request
	var req struct {
		Name             string `json:"name"`
		BreederID        *int   `json:"breeder_id"` // Nullable for new breeders
		NewBreeder       string `json:"new_breeder"`
		Indica           int    `json:"indica"`
		Sativa           int    `json:"sativa"`
		Autoflower       bool   `json:"autoflower"`
		SeedCount        int    `json:"seed_count"`
		Description      string `json:"description"`
		ShortDescription string `json:"short_desc"`
		CycleTime        int    `json:"cycle_time"`
		Url              string `json:"url"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		fieldLogger.WithError(err).Error("Failed to bind JSON")
		apiBadRequest(c, "api_invalid_request_payload")
		return
	}

	// Rescue legacy/typed schemeless URLs (e.g. "www.seedfinder.eu/x") by
	// prepending https:// so they pass validation and persist normalized.
	req.Url = utils.NormalizeWebURL(req.Url)

	if err := validateStrainFields(req.Name, req.Description, req.ShortDescription, req.NewBreeder, req.Url); err != nil {
		apiBadRequest(c, err.Error())
		return
	}

	// Validate Indica and Sativa sum
	if req.Indica+req.Sativa != 100 {
		fieldLogger.Error("Indica and Sativa must sum to 100")
		apiBadRequest(c, "api_indica_sativa_must_sum_100")
		return
	}

	// Open the database
	db := DBFromContext(c)
	store := ConfigStoreFromContext(c)

	breederID, ok := resolveRequestBreeder(c, db, req.BreederID, req.NewBreeder)
	if !ok {
		return
	}

	// Insert the new strain into the database
	stmt := `
		INSERT INTO strain (name, breeder_id, indica, sativa, autoflower, seed_count, description, cycle_time, url, short_desc)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id
	`
	//convert autoflower to int
	var autoflowerInt int
	if req.Autoflower {
		autoflowerInt = 1
	} else {
		autoflowerInt = 0
	}
	var id int
	err := db.QueryRow(stmt, req.Name, breederID, req.Indica, req.Sativa, autoflowerInt, req.SeedCount, req.Description, req.CycleTime, req.Url, req.ShortDescription).Scan(&id)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to insert strain")
		apiInternalError(c, "api_failed_to_add_strain")
		return
	}

	store.SetStrains(GetStrains(db))

	// Respond with success
	c.JSON(http.StatusCreated, gin.H{"id": id, "message": T(c, "api_strain_added")})
}

func GetStrainHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "GetStrainHandler")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fieldLogger.WithError(err).Error("Invalid strain ID")
		apiBadRequest(c, "api_invalid_strain_id")
		return
	}

	// Open the database
	db := DBFromContext(c)

	var strain types.Strain

	err = db.QueryRow(`
        SELECT s.id, s.name, b.name as breeder, b.id as breeder_id, s.indica, s.sativa, s.autoflower, s.description, coalesce(s.short_desc, ''), s.seed_count, coalesce(s.cycle_time, 0), coalesce(s.url, ''), coalesce(s.cannadb_uri, '')
        FROM strain s LEFT OUTER JOIN breeder b on s.breeder_id = b.id
        WHERE s.id = $1`, id).Scan(
		&strain.ID, &strain.Name, &strain.Breeder, &strain.BreederID, &strain.Indica, &strain.Sativa,
		&strain.Autoflower, &strain.Description, &strain.ShortDescription, &strain.SeedCount, &strain.CycleTime, &strain.Url, &strain.CannadbURI)
	if err != nil {
		if err == sql.ErrNoRows {
			apiNotFound(c, "api_strain_not_found")
		} else {
			fieldLogger.WithError(err).Error("Failed to fetch strain")
			apiInternalError(c, "api_failed_to_fetch_strain")
		}
		return
	}

	c.JSON(http.StatusOK, strain)
}

func UpdateStrainHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "UpdateStrainHandler")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fieldLogger.WithError(err).Error("Invalid strain ID")
		apiBadRequest(c, "api_invalid_strain_id")
		return
	}

	var req struct {
		Name             string `json:"name"`
		BreederID        *int   `json:"breeder_id"` // Nullable for new breeders
		NewBreeder       string `json:"new_breeder"`
		Indica           int    `json:"indica"`
		Sativa           int    `json:"sativa"`
		Autoflower       bool   `json:"autoflower"`
		Description      string `json:"description"`
		ShortDescription string `json:"short_desc"`
		SeedCount        int    `json:"seed_count"`
		CycleTime        int    `json:"cycle_time"`
		Url              string `json:"url"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		fieldLogger.WithError(err).Error("Failed to bind JSON")
		apiBadRequest(c, "api_invalid_request_body")
		return
	}

	// Rescue legacy schemeless URLs sent back verbatim by the edit modal
	// (pre-v0.2.0 strains stored URLs without http(s)://) so any field can be
	// updated and the value persists normalized.
	req.Url = utils.NormalizeWebURL(req.Url)

	if err := validateStrainFields(req.Name, req.Description, req.ShortDescription, req.NewBreeder, req.Url); err != nil {
		apiBadRequest(c, err.Error())
		return
	}

	// Validate Indica and Sativa sum
	if req.Indica+req.Sativa != 100 {
		fieldLogger.Error("Indica and Sativa must sum to 100")
		apiBadRequest(c, "api_indica_sativa_must_sum_100")
		return
	}

	// Open the database
	db := DBFromContext(c)

	breederID, ok := resolveRequestBreeder(c, db, req.BreederID, req.NewBreeder)
	if !ok {
		return
	}

	// Update the strain in the database
	updateStmt := `
        UPDATE strain
        SET name = $1, breeder_id = $2, indica = $3, sativa = $4, autoflower = $5, description = $6, seed_count = $7, cycle_time = $8, url = $9, short_desc = $10
        WHERE id = $11
    `
	//Convert autoflower to int
	var autoflowerInt int
	if req.Autoflower {
		autoflowerInt = 1
	} else {
		autoflowerInt = 0
	}
	_, err = db.Exec(updateStmt, req.Name, breederID, req.Indica, req.Sativa,
		autoflowerInt, req.Description, req.SeedCount, req.CycleTime, req.Url, req.ShortDescription, id)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to update strain")
		apiInternalError(c, "api_failed_to_update_strain")
		return
	}

	// Refresh the in-memory strain cache so the UI reflects the update without
	// a restart (AddStrainHandler already does this; Update regressed in the
	// v0.2.0 ConfigStore refactor).
	ConfigStoreFromContext(c).SetStrains(GetStrains(db))

	apiOK(c, "api_strain_updated")
}

func DeleteStrainHandler(c *gin.Context) {
	fieldLogger := logger.Log.WithField("func", "DeleteStrainHandler")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		fieldLogger.WithError(err).Error("Invalid strain ID")
		apiBadRequest(c, "api_invalid_strain_id")
		return
	}

	// Open the database
	db := DBFromContext(c)

	result, err := db.Exec(`DELETE FROM strain WHERE id = $1`, id)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to delete strain")
		apiInternalError(c, "api_failed_to_delete_strain")
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		fieldLogger.Error("Strain not found")
		apiNotFound(c, "api_strain_not_found")
		return
	}

	apiOK(c, "api_strain_deleted")
}

func InStockStrainsHandler(c *gin.Context) {
	db := DBFromContext(c)
	strains, err := getStrainsBySeedCount(db, true)
	if err != nil {
		apiInternalError(c, "api_failed_to_fetch_strains")
		return
	}
	c.JSON(http.StatusOK, strains)
}
func OutOfStockStrainsHandler(c *gin.Context) {
	db := DBFromContext(c)
	strains, err := getStrainsBySeedCount(db, false)
	if err != nil {
		apiInternalError(c, "api_failed_to_fetch_strains")
		return
	}
	c.JSON(http.StatusOK, strains)
}
func getStrainsBySeedCount(db *sql.DB, inStock bool) ([]types.Strain, error) {
	fieldLogger := logger.Log.WithField("func", "getStrainsBySeedCount")
	op := ">"
	if !inStock {
		op = "="
	}
	// string_agg is PostgreSQL-only; SQLite uses GROUP_CONCAT.
	aggExpr := "coalesce(GROUP_CONCAT(sl.parent_name, ', '), '')"
	if model.IsPostgres() {
		aggExpr = "coalesce(string_agg(sl.parent_name, ', ' ORDER BY sl.parent_name), '')"
	}

	query := `
		SELECT s.id, s.name, b.name AS breeder, b.id as breeder_id,
		       s.indica, s.sativa, s.autoflower, s.seed_count, s.description,
		       coalesce(s.short_desc, ''), coalesce(s.cycle_time, 0), coalesce(s.url, ''),
		       ` + aggExpr + `
		FROM strain s
		JOIN breeder b ON s.breeder_id = b.id
		LEFT JOIN strain_lineage sl ON sl.strain_id = s.id
		WHERE s.seed_count ` + op + ` 0
		GROUP BY s.id, s.name, b.name, b.id, s.indica, s.sativa, s.autoflower,
		         s.seed_count, s.description, s.short_desc, s.cycle_time, s.url
		ORDER BY s.name ASC
	`

	rows, err := db.Query(query)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to query strains")
		return nil, err
	}
	defer rows.Close()

	var strains []types.Strain
	for rows.Next() {
		var strain types.Strain
		if err := rows.Scan(&strain.ID, &strain.Name, &strain.Breeder, &strain.BreederID,
			&strain.Indica, &strain.Sativa, &strain.Autoflower, &strain.SeedCount,
			&strain.Description, &strain.ShortDescription, &strain.CycleTime, &strain.Url,
			&strain.Lineage); err != nil {
			fieldLogger.WithError(err).Error("Failed to scan strain")
			return nil, err
		}
		strains = append(strains, strain)
	}

	return strains, nil
}

func PlantsByStrainHandler(context *gin.Context) {
	fieldLogger := logger.Log.WithField("handler", "PlantsByStrainHandler")

	// Parse strain ID from query parameter
	strainID, err := strconv.Atoi(context.Param("strainID"))
	if err != nil {
		fieldLogger.WithError(err).Error("Invalid strain ID")
		apiBadRequest(context, "api_invalid_strain_id")
		return
	}

	fieldLogger = fieldLogger.WithField("strainID", strainID)
	fieldLogger.Info("Fetching plants for strain")

	db := DBFromContext(context)

	// Query plants with the given strain ID
	rows, err := db.Query(`SELECT id, name FROM plant WHERE strain_id = $1 ORDER BY name ASC`, strainID)
	if err != nil {
		fieldLogger.WithError(err).Error("Failed to query database")
		apiInternalError(context, "api_failed_to_fetch_plants")
		return
	}
	defer rows.Close()

	// Parse query results
	var plants []types.Plant
	for rows.Next() {
		var plant types.Plant
		if err := rows.Scan(&plant.ID, &plant.Name); err != nil {
			fieldLogger.WithError(err).Error("Failed to scan row")
			apiInternalError(context, "api_failed_to_process_results")
			return
		}
		plants = append(plants, plant)
	}

	if err := rows.Err(); err != nil {
		fieldLogger.WithError(err).Error("Error iterating rows")
		apiInternalError(context, "api_failed_to_process_results")
		return
	}

	// Return the list of plants as JSON
	fieldLogger.WithField("plantCount", len(plants)).Info("Plants fetched successfully")
	context.JSON(http.StatusOK, plants)
}
