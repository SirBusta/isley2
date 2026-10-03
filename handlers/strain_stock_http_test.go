package handlers_test

// Stock tracking through the real pages/API: the Wanted list and the date
// seeds were added (see handlers/strain_stock.go for the rules).

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

func (e *strainEditEnv) stock(t *testing.T) (count int, wanted bool, added sql.NullString) {
	t.Helper()
	var w int
	require.NoError(t, e.db.QueryRow("SELECT seed_count, wanted, seeds_added_on FROM strain WHERE id = $1", e.strainID).
		Scan(&count, &w, &added))
	return count, w == 1, added
}

func (e *strainEditEnv) getBody(t *testing.T, path string) string {
	t.Helper()
	resp := e.client.Get(path)
	defer testutil.DrainAndClose(resp)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func (e *strainEditEnv) setWanted(t *testing.T, id int, wanted bool) (int, bool) {
	t.Helper()
	resp := e.client.SessionPostJSON(t, "/strains/"+strconv.Itoa(id)+"/wanted", e.token, map[string]any{"wanted": wanted})
	defer testutil.DrainAndClose(resp)
	var out struct {
		Wanted bool `json:"wanted"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Wanted
}

func TestStrainStock_EditFollowsSeedCount(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	today := time.Now().Format("2006-01-02")

	// Out of stock and wanted.
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_count": 0, "wanted": true}))
	_, wanted, added := e.stock(t)
	assert.True(t, wanted)
	assert.False(t, added.Valid)

	// Restocked without a date: dated today, no longer wanted.
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_count": 10, "wanted": true, "seeds_added_on": ""}))
	_, wanted, added = e.stock(t)
	assert.False(t, wanted, "seeds in stock can't be wanted")
	assert.Equal(t, today, added.String)

	// The user corrects the date; a later save without the field keeps it.
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_count": 10, "seeds_added_on": "2024-04-20"}))
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_count": 9}))
	_, _, added = e.stock(t)
	assert.Equal(t, "2024-04-20", added.String)

	page := e.getBody(t, "/strain/"+strconv.Itoa(e.strainID))
	assert.Contains(t, page, "2024-04-20")
	assert.Regexp(t, `\(\d+ (mo|yr \d+ mo)\)`, page, "shows how old the seeds are")

	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"seed_count": 9, "seeds_added_on": "20/04/2024"}))

	// Used up: the date goes.
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"seed_count": 0, "seeds_added_on": "2024-04-20"}))
	_, _, added = e.stock(t)
	assert.False(t, added.Valid)
}

func TestStrainStock_WantedButtonAndViews(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	inStock := testutil.SeedStrain(t, e.db, e.breeder, "Has Seeds")
	testutil.MustExec(t, e.db, "UPDATE strain SET seed_count = 3 WHERE id = $1", inStock)
	usedUp := testutil.SeedStrain(t, e.db, e.breeder, "Used Up")
	testutil.MustExec(t, e.db, "UPDATE strain SET seed_count = 0 WHERE id = $1", usedUp)
	testutil.MustExec(t, e.db, "UPDATE strain SET seed_count = 0 WHERE id = $1", e.strainID)

	status, wanted := e.setWanted(t, e.strainID, true)
	require.Equal(t, http.StatusOK, status)
	assert.True(t, wanted)

	status, wanted = e.setWanted(t, inStock, true)
	require.Equal(t, http.StatusOK, status)
	assert.False(t, wanted, "a strain with seeds can't be wanted")

	status, _ = e.setWanted(t, 99999, true)
	assert.Equal(t, http.StatusNotFound, status)

	names := func(view string) []string {
		var list []struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal([]byte(e.getBody(t, "/strains/"+view)), &list))
		var out []string
		for _, s := range list {
			out = append(out, s.Name)
		}
		return out
	}
	assert.Equal(t, []string{"Has Seeds"}, names("in-stock"))
	assert.Equal(t, []string{"Edit Me"}, names("wanted"))
	assert.Equal(t, []string{"Used Up"}, names("out-of-stock"))

	assert.Contains(t, e.getBody(t, "/strain/"+strconv.Itoa(e.strainID)), "Remove from Wanted")

	status, wanted = e.setWanted(t, e.strainID, false)
	require.Equal(t, http.StatusOK, status)
	assert.False(t, wanted)
	assert.Equal(t, []string{"Edit Me", "Used Up"}, names("out-of-stock"))
}

func TestStrainStock_AddStrainDatesNewSeeds(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	resp := e.client.SessionPostJSON(t, "/strains", e.token, map[string]any{
		"name": "Fresh", "breeder_id": e.breeder, "indica": 50, "sativa": 50,
		"cycle_time": 56, "short_desc": "x", "seed_count": 5, "wanted": true,
	})
	defer testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var out struct {
		ID int `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))

	var wanted int
	var added sql.NullString
	require.NoError(t, e.db.QueryRow("SELECT wanted, seeds_added_on FROM strain WHERE id = $1", out.ID).Scan(&wanted, &added))
	assert.Equal(t, 0, wanted)
	assert.Equal(t, time.Now().Format("2006-01-02"), added.String)
}

func TestStrainStock_UsingTheLastSeedClearsTheDate(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	zone := testutil.SeedZone(t, e.db, "Tent")
	testutil.MustExec(t, e.db, "UPDATE strain SET seed_count = 2, seeds_added_on = '2025-06-01' WHERE id = $1", e.strainID)

	addPlant := func(name string) {
		resp := e.client.SessionPostJSON(t, "/plants", e.token, map[string]any{
			"name": name, "zone_id": zone, "strain_id": e.strainID, "status_id": 1,
			"date": "2026-10-01", "decrement_seed_count": true,
		})
		defer testutil.DrainAndClose(resp)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	}

	addPlant("One")
	count, _, added := e.stock(t)
	assert.Equal(t, 1, count)
	assert.Equal(t, "2025-06-01", added.String, "seeds left: date kept")

	addPlant("Two")
	count, _, added = e.stock(t)
	assert.Equal(t, 0, count)
	assert.False(t, added.Valid, "last seed used: date cleared")
}

func TestImportReview_NewImportStartsWanted(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	_, page := s.reviewPage(t, url.Values{"source": {"straincompass"}, "slug": {"blue-dream"}, "name": {"Blue Dream"}, "listing_breeder": {"Seed Supreme"}})
	assert.Regexp(t, `id="editWanted" checked`, page)
}
