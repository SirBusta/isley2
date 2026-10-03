package handlers_test

// Editing a strain's cannabinoid ranges and effects/flavors/terpenes/medical
// uses through PUT /strains/:id (the Edit Strain page's save).

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

const strainEditPassword = "strain-edit-pw"

type strainEditEnv struct {
	db       *sql.DB
	client   *testutil.Client
	token    string
	strainID int
	breeder  int
}

func newStrainEditEnv(t *testing.T) *strainEditEnv {
	t.Helper()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	testutil.SeedAdmin(t, db, strainEditPassword)
	breeder := testutil.SeedBreeder(t, db, "Edit Breeder")
	strainID := testutil.SeedStrain(t, db, breeder, "Edit Me")
	c, token := server.LoginAndFetchCSRF(t, strainEditPassword, "/strain/"+strconv.Itoa(strainID)+"/edit")
	return &strainEditEnv{db: db, client: c, token: token, strainID: strainID, breeder: breeder}
}

func (e *strainEditEnv) put(t *testing.T, extra map[string]any) int {
	t.Helper()
	body := map[string]any{
		"name": "Edit Me", "breeder_id": e.breeder, "indica": 50, "sativa": 50,
		"autoflower": false, "seed_count": 0, "cycle_time": 60, "short_desc": "x",
	}
	for k, v := range extra {
		body[k] = v
	}
	resp := e.client.SessionPutJSON(t, "/strains/"+strconv.Itoa(e.strainID), e.token, body)
	defer testutil.DrainAndClose(resp)
	return resp.StatusCode
}

func (e *strainEditEnv) names(t *testing.T, table string) []string {
	t.Helper()
	rows, err := e.db.Query("SELECT name FROM "+table+" WHERE strain_id = $1 ORDER BY id", e.strainID)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		out = append(out, n)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestStrainEdit_SavesCannabinoidsAndAttributes(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)

	status := e.put(t, map[string]any{
		"cannabinoids": map[string]any{"thc_min": 18, "thc_max": 24.5, "cbd_max": 1, "cbn_max": nil},
		"attributes": map[string]any{
			"effects":      []map[string]any{{"name": "Relaxed", "intensity": 0.8}, {"name": " relaxed "}, {"name": "Happy"}},
			"flavors":      []map[string]any{{"name": "Citrus"}},
			"terpenes":     []map[string]any{{"name": "Myrcene", "level": "high"}, {"name": ""}},
			"medical_uses": []map[string]any{},
		},
	})
	require.Equal(t, http.StatusOK, status)

	var thcMin, thcMax, cbdMax sql.NullFloat64
	var cbnMax sql.NullFloat64
	require.NoError(t, e.db.QueryRow("SELECT thc_min, thc_max, cbd_max, cbn_max FROM strain WHERE id = $1", e.strainID).
		Scan(&thcMin, &thcMax, &cbdMax, &cbnMax))
	assert.Equal(t, 18.0, thcMin.Float64)
	assert.Equal(t, 24.5, thcMax.Float64)
	assert.Equal(t, 1.0, cbdMax.Float64)
	assert.False(t, cbnMax.Valid, "null clears the value")

	assert.Equal(t, []string{"Relaxed", "Happy"}, e.names(t, "strain_effect"), "trimmed, blanks dropped, case-insensitive duplicates collapsed")
	assert.Equal(t, []string{"Citrus"}, e.names(t, "strain_flavor"))
	assert.Equal(t, []string{"Myrcene"}, e.names(t, "strain_terpene"))

	var intensity sql.NullFloat64
	var level sql.NullString
	require.NoError(t, e.db.QueryRow("SELECT intensity FROM strain_effect WHERE strain_id = $1 AND name = 'Relaxed'", e.strainID).Scan(&intensity))
	require.NoError(t, e.db.QueryRow("SELECT level FROM strain_terpene WHERE strain_id = $1", e.strainID).Scan(&level))
	assert.Equal(t, 0.8, intensity.Float64, "intensity round-trips")
	assert.Equal(t, "high", level.String, "level round-trips")
}

func TestStrainEdit_OmittedBlocksLeaveDataAlone(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	testutil.MustExec(t, e.db, "UPDATE strain SET thc_max = 20 WHERE id = $1", e.strainID)
	testutil.MustExec(t, e.db, "INSERT INTO strain_flavor (strain_id, name) VALUES ($1, 'Berry')", e.strainID)

	require.Equal(t, http.StatusOK, e.put(t, nil))

	var thcMax sql.NullFloat64
	require.NoError(t, e.db.QueryRow("SELECT thc_max FROM strain WHERE id = $1", e.strainID).Scan(&thcMax))
	assert.Equal(t, 20.0, thcMax.Float64)
	assert.Equal(t, []string{"Berry"}, e.names(t, "strain_flavor"))
}

func TestStrainEdit_SavesAndClearsGrowingInfo(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{
		"growing": map[string]any{"height_indoor": " 80-120 cm ", "yield_indoor": "400 g/m²", "height_outdoor": "", "yield_outdoor": ""},
	}))
	var hIn, yIn, hOut sql.NullString
	require.NoError(t, e.db.QueryRow("SELECT height_indoor, yield_indoor, height_outdoor FROM strain WHERE id = $1", e.strainID).Scan(&hIn, &yIn, &hOut))
	assert.Equal(t, "80-120 cm", hIn.String)
	assert.Equal(t, "400 g/m²", yIn.String)
	assert.False(t, hOut.Valid, "empty is stored as NULL")

	// Omitting the block leaves it alone; sending blanks clears it.
	require.Equal(t, http.StatusOK, e.put(t, nil))
	require.NoError(t, e.db.QueryRow("SELECT height_indoor FROM strain WHERE id = $1", e.strainID).Scan(&hIn))
	assert.Equal(t, "80-120 cm", hIn.String)

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"growing": map[string]any{"height_indoor": ""}}))
	require.NoError(t, e.db.QueryRow("SELECT height_indoor FROM strain WHERE id = $1", e.strainID).Scan(&hIn))
	assert.False(t, hIn.Valid)
}

func TestStrainEdit_SeedLocation(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	loc := func() sql.NullString {
		var s sql.NullString
		require.NoError(t, e.db.QueryRow("SELECT seed_location FROM strain WHERE id = $1", e.strainID).Scan(&s))
		return s
	}

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_location": "  Jar 1 (fridge) "}))
	assert.Equal(t, "Jar 1 (fridge)", loc().String)

	require.Equal(t, http.StatusOK, e.put(t, nil))
	assert.Equal(t, "Jar 1 (fridge)", loc().String, "omitted = unchanged")

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_location": ""}))
	assert.False(t, loc().Valid, "blank clears it")
}

func TestStrainEdit_SeedType(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	seedType := func() sql.NullString {
		var s sql.NullString
		require.NoError(t, e.db.QueryRow("SELECT seed_type FROM strain WHERE id = $1", e.strainID).Scan(&s))
		return s
	}
	page := func(path string) string {
		resp := e.client.Get(path)
		defer testutil.DrainAndClose(resp)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(body)
	}

	assert.False(t, seedType().Valid, "not set by default")
	assert.Regexp(t, `<option value="" selected>Not set`, page("/strain/"+strconv.Itoa(e.strainID)+"/edit"))

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_type": " Feminized "}))
	assert.Equal(t, "feminized", seedType().String, "trimmed and lower-cased")
	assert.Regexp(t, `<option value="feminized" selected>`, page("/strain/"+strconv.Itoa(e.strainID)+"/edit"))
	assert.Contains(t, page("/strain/"+strconv.Itoa(e.strainID)), "Feminized Seed", "shown on the strain page")

	require.Equal(t, http.StatusOK, e.put(t, nil))
	assert.Equal(t, "feminized", seedType().String, "omitted = unchanged")

	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"seed_type": "autoflower"}))
	assert.Equal(t, "feminized", seedType().String)

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_type": ""}))
	assert.False(t, seedType().Valid, "blank clears it")
}

func TestStrainAdd_SeedType(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	add := func(seedType string) (int, int) {
		resp := e.client.SessionPostJSON(t, "/strains", e.token, map[string]any{
			"name": "New " + seedType, "breeder_id": e.breeder, "indica": 50, "sativa": 50,
			"cycle_time": 56, "short_desc": "x", "seed_type": seedType,
		})
		defer testutil.DrainAndClose(resp)
		var out struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.ID
	}

	status, id := add("clone")
	require.Equal(t, http.StatusCreated, status)
	var got sql.NullString
	require.NoError(t, e.db.QueryRow("SELECT seed_type FROM strain WHERE id = $1", id).Scan(&got))
	assert.Equal(t, "clone", got.String)

	status, _ = add("autoflower")
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestStrainEdit_RejectsBadCannabinoids(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)

	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"cannabinoids": map[string]any{"thc_max": 120}}))
	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"cannabinoids": map[string]any{"thc_min": -1}}))
	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"cannabinoids": map[string]any{"thc_min": 25, "thc_max": 20}}))
	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"cannabinoids": map[string]any{"cbd_min": 5, "cbd_max": 1}}))
}

func TestStrainEdit_RejectsTooManyAttributes(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)

	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = map[string]any{"name": "Effect " + strconv.Itoa(i)}
	}
	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"attributes": map[string]any{"effects": many}}))
	assert.Empty(t, e.names(t, "strain_effect"))
}
