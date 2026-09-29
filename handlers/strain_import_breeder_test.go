package handlers_test

// Import write path for StrainCompass and CannaDB, against fake upstream
// servers: the user-chosen breeder wins over the listing's, and the same
// source strain imported for two breeders becomes two strain rows.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

const importBreederPassword = "import-breeder-pw"

const fakeStraincompassList = `{"strains":[{"slug":"blue-dream","name":"Blue Dream","breeder":"Seed Supreme","indicaPercent":40,"sativaPercent":60,"floweringTimeMax":9,"lineage":"Blueberry x (Haze x Super Silver Haze)","heightIndoor":" 90-150cm ","yieldIndoor":"450-550 g/m²","heightOutdoor":null,"yieldOutdoor":null}],"total":1}`

const fakeCannadbURI = "at://did:plc:test/org.cannadb.strain/runtz"

const fakeCannadbStrain = `{"uri":"` + fakeCannadbURI + `","indexedAt":"2026-01-01T00:00:00Z","value":{"name":"Runtz","breederName":"Cookies","indicaSativa":50,"parentNames":["Zkittlez","Gelato"]}}`

// Gelato has no lineage on StrainCompass; CannaDB has it (plus a near-miss
// "Gelato #33" that must not be matched).
const fakeStraincompassGelato = `{"strains":[{"slug":"gelato","name":"Gelato","breeder":"Seed Supreme","lineage":""}],"total":1}`

const fakeCannadbGelatoURI = "at://did:plc:test/org.cannadb.strain/gelato"

const fakeCannadbGelatoSearch = `{"strains":[
	{"uri":"at://did:plc:test/org.cannadb.strain/gelato33","name":"Gelato #33","breederName":"Cookies"},
	{"uri":"` + fakeCannadbGelatoURI + `","name":"Gelato","breederName":"Cookies"}]}`

const fakeCannadbGelato = `{"uri":"` + fakeCannadbGelatoURI + `","indexedAt":"2026-01-01T00:00:00Z","value":{"name":"Gelato","breederName":"Cookies","parentNames":["Sunset Sherbet","Thin Mint GSC"]}}`

type importServer struct {
	db     *sql.DB
	server *testutil.TestServer
	client *testutil.Client
	token  string
}

func newImportServer(t *testing.T) *importServer {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch r.URL.Path {
		case "/sc/api/strains":
			if q.Get("q") == "Gelato" {
				_, _ = w.Write([]byte(fakeStraincompassGelato))
			} else {
				_, _ = w.Write([]byte(fakeStraincompassList))
			}
		case "/cannadb/xrpc/org.cannadb.searchStrains":
			if q.Get("q") == "Gelato" {
				_, _ = w.Write([]byte(fakeCannadbGelatoSearch))
			} else {
				_, _ = w.Write([]byte(`{"strains":[]}`))
			}
		case "/cannadb/xrpc/org.cannadb.getStrain":
			if q.Get("uri") == fakeCannadbGelatoURI {
				_, _ = w.Write([]byte(fakeCannadbGelato))
			} else {
				_, _ = w.Write([]byte(fakeCannadbStrain))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	server.ConfigStore.SetStraincompassEnabled(1)
	server.ConfigStore.SetStraincompassBaseURL(upstream.URL + "/sc/api/")
	server.ConfigStore.SetCannadbEnabled(1)
	server.ConfigStore.SetCannadbBaseURL(upstream.URL + "/cannadb/xrpc/")
	testutil.SeedAdmin(t, db, importBreederPassword)

	c, token := server.LoginAndFetchCSRF(t, importBreederPassword, "/strains")
	return &importServer{db: db, server: server, client: c, token: token}
}

type importResult struct {
	ID        int    `json:"id"`
	BreederID int    `json:"breeder_id"`
	Breeder   string `json:"breeder"`
}

func (s *importServer) post(t *testing.T, path string, body map[string]any) (int, importResult) {
	t.Helper()
	resp := s.client.SessionPostJSON(t, path, s.token, body)
	defer testutil.DrainAndClose(resp)
	var got importResult
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	}
	return resp.StatusCode, got
}

func (s *importServer) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow(query, args...).Scan(&n))
	return n
}

func TestStraincompassImport_ChosenBreederWins(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	mine := testutil.SeedBreeder(t, s.db, "Sensi Seeds")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": mine,
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, mine, got.BreederID)
	assert.Equal(t, "Sensi Seeds", got.Breeder)
	assert.Equal(t, 0, s.count(t, "SELECT COUNT(*) FROM breeder WHERE name = 'Seed Supreme'"),
		"the listing's breeder must not be created when the user chose one")
}

func TestStraincompassImport_SameStrainTwoBreedersIsTwoRows(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	first := testutil.SeedBreeder(t, s.db, "Breeder A")

	status, a := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": first,
	})
	require.Equal(t, http.StatusOK, status)

	status, b := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "new_breeder": "Breeder B",
	})
	require.Equal(t, http.StatusOK, status)
	assert.NotEqual(t, a.ID, b.ID, "a second breeder must get its own strain row")
	assert.Equal(t, 2, s.count(t, "SELECT COUNT(*) FROM strain WHERE straincompass_slug = 'blue-dream'"))

	// Re-importing for an existing breeder updates that breeder's row in place.
	status, again := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": first,
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, a.ID, again.ID)
	assert.Equal(t, 2, s.count(t, "SELECT COUNT(*) FROM strain WHERE straincompass_slug = 'blue-dream'"))
	assert.Equal(t, first, s.count(t, "SELECT breeder_id FROM strain WHERE id = $1", a.ID),
		"the first row keeps its breeder")
}

func TestStraincompassImport_NoChoiceFallsBackToListingBreeder(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream",
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Seed Supreme", got.Breeder)
}

func TestStraincompassImport_UnknownBreederIDRejected(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	status, _ := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": 9999,
	})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, 0, s.count(t, "SELECT COUNT(*) FROM strain"))
}

func TestStraincompassPreview_ReturnsListingBreeder(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	resp := s.client.Get("/strains/straincompass/preview?slug=blue-dream&name=Blue+Dream")
	defer testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got struct {
		Name    string `json:"name"`
		Breeder string `json:"breeder"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "Blue Dream", got.Name)
	assert.Equal(t, "Seed Supreme", got.Breeder)
}

func TestStraincompassImport_RecordsLineageFromNote(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Local Breeder")
	blueberry := testutil.SeedStrain(t, s.db, breeder, "Blueberry")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)

	rows, err := s.db.Query("SELECT parent_name, parent_strain_id FROM strain_lineage WHERE strain_id = $1 ORDER BY parent_name", got.ID)
	require.NoError(t, err)
	defer rows.Close()
	type parent struct {
		name string
		link sql.NullInt64
	}
	var parents []parent
	for rows.Next() {
		var p parent
		require.NoError(t, rows.Scan(&p.name, &p.link))
		parents = append(parents, p)
	}
	require.NoError(t, rows.Err())
	require.Len(t, parents, 2)
	assert.Equal(t, "Blueberry", parents[0].name)
	assert.Equal(t, int64(blueberry), parents[0].link.Int64, "a parent matching one local strain is linked")
	assert.Equal(t, "Haze x Super Silver Haze", parents[1].name)
	assert.False(t, parents[1].link.Valid)
}

func TestStraincompassImport_KeepsExistingLineage(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Local Breeder")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)

	// The user replaces the parsed lineage with their own.
	testutil.MustExec(t, s.db, "DELETE FROM strain_lineage WHERE strain_id = $1", got.ID)
	testutil.MustExec(t, s.db, "INSERT INTO strain_lineage (strain_id, parent_name) VALUES ($1, 'My Own Parent')", got.ID)

	status, _ = s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)

	var n int
	var name string
	require.NoError(t, s.db.QueryRow("SELECT COUNT(*), MAX(parent_name) FROM strain_lineage WHERE strain_id = $1", got.ID).Scan(&n, &name))
	assert.Equal(t, 1, n)
	assert.Equal(t, "My Own Parent", name, "re-import must not overwrite the user's lineage")
}

func TestStraincompassImport_KeepsHandEnteredAttributes(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Local Breeder")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)
	testutil.MustExec(t, s.db, "INSERT INTO strain_terpene (strain_id, name) VALUES ($1, 'Pinene')", got.ID)

	// The fake record has no "sourced" attribute groups, so a re-import must
	// leave the user's terpene in place.
	status, _ = s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, 1, s.count(t, "SELECT COUNT(*) FROM strain_terpene WHERE strain_id = $1 AND name = 'Pinene'", got.ID))
}

func TestStraincompassImport_HeightAndYield(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Local Breeder")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)

	var hIn, yIn, hOut sql.NullString
	require.NoError(t, s.db.QueryRow("SELECT height_indoor, yield_indoor, height_outdoor FROM strain WHERE id = $1", got.ID).Scan(&hIn, &yIn, &hOut))
	assert.Equal(t, "90-150cm", hIn.String, "trimmed")
	assert.Equal(t, "450-550 g/m²", yIn.String)
	assert.False(t, hOut.Valid, "missing upstream value stays NULL")

	// Values the user typed in survive a re-import that has none for them.
	testutil.MustExec(t, s.db, "UPDATE strain SET height_outdoor = '2-3 m', thc_min = 18 WHERE id = $1", got.ID)
	status, _ = s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)
	var thcMin sql.NullFloat64
	require.NoError(t, s.db.QueryRow("SELECT height_outdoor, thc_min FROM strain WHERE id = $1", got.ID).Scan(&hOut, &thcMin))
	assert.Equal(t, "2-3 m", hOut.String)
	assert.Equal(t, 18.0, thcMin.Float64)
}

func lineageRows(t *testing.T, db *sql.DB, strainID int) (names []string, source, uri string) {
	t.Helper()
	rows, err := db.Query("SELECT parent_name FROM strain_lineage WHERE strain_id = $1 ORDER BY parent_name", strainID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	require.NoError(t, rows.Err())
	var src, u sql.NullString
	require.NoError(t, db.QueryRow("SELECT lineage_source, lineage_source_uri FROM strain WHERE id = $1", strainID).Scan(&src, &u))
	return names, src.String, u.String
}

func TestStraincompassImport_FillsLineageFromCannadb(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Cookies")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "gelato", "name": "Gelato", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)

	names, source, uri := lineageRows(t, s.db, got.ID)
	assert.Equal(t, []string{"Sunset Sherbet", "Thin Mint GSC"}, names, "exact-name CannaDB match fills the empty lineage, not Gelato #33")
	assert.Equal(t, "cannadb", source)
	assert.Equal(t, fakeCannadbGelatoURI, uri)
}

func TestStraincompassImport_NoCannadbFillWhenDisabled(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	s.server.ConfigStore.SetCannadbEnabled(0)
	breeder := testutil.SeedBreeder(t, s.db, "Cookies")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "gelato", "name": "Gelato", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)
	names, _, _ := lineageRows(t, s.db, got.ID)
	assert.Empty(t, names)
}

func TestStraincompassImport_StraincompassLineageWinsOverCannadb(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Local Breeder")

	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "blue-dream", "name": "Blue Dream", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)
	_, source, _ := lineageRows(t, s.db, got.ID)
	assert.Equal(t, "straincompass", source)
}

func TestLineageSource_ClearedOnlyWhenUserChangesParents(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Cookies")
	status, got := s.post(t, "/strains/straincompass/import", map[string]any{
		"slug": "gelato", "name": "Gelato", "breeder_id": breeder,
	})
	require.Equal(t, http.StatusOK, status)
	path := "/strains/" + strconv.Itoa(got.ID) + "/lineage"

	// Saving the same parents (what Edit Strain does on every save) keeps the source.
	resp := s.client.SessionPutJSON(t, path, s.token, map[string]any{"parents": []map[string]any{
		{"parent_name": "Thin Mint GSC"}, {"parent_name": "Sunset Sherbet"},
	}})
	testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, source, _ := lineageRows(t, s.db, got.ID)
	assert.Equal(t, "cannadb", source)

	// A real change makes it the user's own.
	resp = s.client.SessionPutJSON(t, path, s.token, map[string]any{"parents": []map[string]any{
		{"parent_name": "Sunset Sherbet"},
	}})
	testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, source, _ = lineageRows(t, s.db, got.ID)
	assert.Empty(t, source)
}

func TestCannadbImport_KeepsUserLineage(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Cookies")

	status, got := s.post(t, "/strains/cannadb/import", map[string]any{"uri": fakeCannadbURI, "breeder_id": breeder})
	require.Equal(t, http.StatusOK, status)
	names, source, _ := lineageRows(t, s.db, got.ID)
	assert.Equal(t, []string{"Gelato", "Zkittlez"}, names)
	assert.Equal(t, "cannadb", source)

	// User replaces the lineage; a re-import must leave it alone.
	resp := s.client.SessionPutJSON(t, "/strains/"+strconv.Itoa(got.ID)+"/lineage", s.token,
		map[string]any{"parents": []map[string]any{{"parent_name": "My Own Parent"}}})
	testutil.DrainAndClose(resp)
	status, _ = s.post(t, "/strains/cannadb/import", map[string]any{"uri": fakeCannadbURI, "breeder_id": breeder})
	require.Equal(t, http.StatusOK, status)
	names, _, _ = lineageRows(t, s.db, got.ID)
	assert.Equal(t, []string{"My Own Parent"}, names)
}

func TestCannadbImport_SameStrainTwoBreedersIsTwoRows(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	first := testutil.SeedBreeder(t, s.db, "Breeder A")

	status, a := s.post(t, "/strains/cannadb/import", map[string]any{
		"uri": fakeCannadbURI, "breeder_id": first,
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, first, a.BreederID)

	status, b := s.post(t, "/strains/cannadb/import", map[string]any{
		"uri": fakeCannadbURI, "new_breeder": "Breeder B",
	})
	require.Equal(t, http.StatusOK, status)
	assert.NotEqual(t, a.ID, b.ID)
	assert.Equal(t, 2, s.count(t, "SELECT COUNT(*) FROM strain WHERE cannadb_uri = $1", fakeCannadbURI))
	assert.Equal(t, 0, s.count(t, "SELECT COUNT(*) FROM breeder WHERE name = 'Cookies'"),
		"CannaDB's breeder must not be created when the user chose one")
}

func TestCannadbImport_NoChoiceFallsBackToRecordBreeder(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	status, got := s.post(t, "/strains/cannadb/import", map[string]any{"uri": fakeCannadbURI})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Cookies", got.Breeder)
}
