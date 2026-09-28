package handlers_test

// Breeder find-or-create and the breeder reference list. Typed breeder
// names (Add/Edit Strain, Add Plant's inline strain, Settings) must reuse
// an existing breeder when the name matches case-insensitively instead of
// inserting a duplicate row.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/handlers"
	"isley/tests/testutil"
)

const breederDedupePassword = "breeder-dedupe-pw"

func TestBreederDedupe_AddStrainReusesExistingBreeder(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	testutil.SeedAdmin(t, db, breederDedupePassword)
	existingID := testutil.SeedBreeder(t, db, "Seed Junky Genetics")

	c, token := server.LoginAndFetchCSRF(t, breederDedupePassword, "/strain/new")
	resp := c.SessionPostJSON(t, "/strains", token, map[string]any{
		"name":        "Permanent Marker",
		"breeder_id":  nil,
		"new_breeder": "  seed junky GENETICS ",
		"indica":      50,
		"sativa":      50,
		"seed_count":  0,
	})
	defer testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got struct {
		ID int `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	var breederID, n int
	require.NoError(t, db.QueryRow("SELECT breeder_id FROM strain WHERE id = $1", got.ID).Scan(&breederID))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM breeder").Scan(&n))
	assert.Equal(t, existingID, breederID, "strain should use the existing breeder")
	assert.Equal(t, 1, n, "no duplicate breeder row")
}

func TestBreederDedupe_AddStrainCreatesTrimmedNewBreeder(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	testutil.SeedAdmin(t, db, breederDedupePassword)

	c, token := server.LoginAndFetchCSRF(t, breederDedupePassword, "/strain/new")
	resp := c.SessionPostJSON(t, "/strains", token, map[string]any{
		"name":        "Runtz",
		"breeder_id":  nil,
		"new_breeder": "  Cookies Fam  ",
		"indica":      50,
		"sativa":      50,
	})
	defer testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var name string
	require.NoError(t, db.QueryRow("SELECT name FROM breeder").Scan(&name))
	assert.Equal(t, "Cookies Fam", name)
}

func TestBreederDedupe_AddStrainRejectsWhitespaceBreeder(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	testutil.SeedAdmin(t, db, breederDedupePassword)

	c, token := server.LoginAndFetchCSRF(t, breederDedupePassword, "/strain/new")
	resp := c.SessionPostJSON(t, "/strains", token, map[string]any{
		"name":        "Runtz",
		"breeder_id":  nil,
		"new_breeder": "   ",
		"indica":      50,
		"sativa":      50,
	})
	defer testutil.DrainAndClose(resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestBreederDedupe_AddBreederReturnsExisting(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	testutil.SeedAdmin(t, db, breederDedupePassword)
	existingID := testutil.SeedBreeder(t, db, "Sensi Seeds")

	c, token := server.LoginAndFetchCSRF(t, breederDedupePassword, "/settings")
	resp := c.SessionPostJSON(t, "/breeders", token, map[string]string{"breeder_name": "sensi seeds"})
	defer testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		ID       int  `json:"id"`
		Existing bool `json:"existing"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, existingID, got.ID)
	assert.True(t, got.Existing)

	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM breeder").Scan(&n))
	assert.Equal(t, 1, n)
}

func TestBreederDedupe_CreateNewStrainReusesExistingBreeder(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	existingID := testutil.SeedBreeder(t, db, "Barneys Farm")

	id, err := handlers.CreateNewStrain(db, nil, &struct {
		Name       string `json:"name"`
		BreederId  int    `json:"breeder_id"`
		NewBreeder string `json:"new_breeder"`
	}{Name: "Moby Dick", NewBreeder: "BARNEYS FARM"})
	require.NoError(t, err)

	var breederID, n int
	require.NoError(t, db.QueryRow("SELECT breeder_id FROM strain WHERE id = $1", id).Scan(&breederID))
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM breeder").Scan(&n))
	assert.Equal(t, existingID, breederID)
	assert.Equal(t, 1, n)
}

func TestBreederDedupe_GetBreedersSortedCaseInsensitively(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	testutil.SeedBreeder(t, db, "zeta")
	testutil.SeedBreeder(t, db, "Alpha")
	testutil.SeedBreeder(t, db, "beta")

	got := handlers.GetBreeders(db)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"Alpha", "beta", "zeta"}, []string{got[0].Name, got[1].Name, got[2].Name})
}

func TestBreederReference_ServesBundledList(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	testutil.SeedAdmin(t, db, breederDedupePassword)

	c := server.LoginAsAdmin(t, breederDedupePassword)
	resp := c.Get("/breeders/reference")
	defer testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got struct {
		Names []string `json:"names"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Greater(t, len(got.Names), 1000, "bundled snapshot has ~1,172 names")
	assert.Contains(t, got.Names, "Sensi Seeds")
	assert.True(t, sort.SliceIsSorted(got.Names, func(i, j int) bool {
		return strings.ToLower(got.Names[i]) < strings.ToLower(got.Names[j])
	}), "names sorted case-insensitively")
	for _, n := range got.Names {
		assert.Equal(t, strings.TrimSpace(n), n)
		assert.NotEmpty(t, n)
	}
}

func TestBreederReference_RequiresLogin(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)

	c := server.NewClient(t)
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/breeders/reference", nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer testutil.DrainAndClose(resp)
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, resp.StatusCode)
}
