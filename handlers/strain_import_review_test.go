package handlers_test

// Review-before-save imports: GET /strain/import/review builds a pre-filled
// Edit Strain page without writing anything; POST /strains/import/save adds
// or updates the strain. Uses the fake StrainCompass/CannaDB servers from
// strain_import_breeder_test.go.

import (
	"database/sql"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

func (s *importServer) reviewPage(t *testing.T, query url.Values) (int, string) {
	t.Helper()
	resp := s.client.Get("/strain/import/review?" + query.Encode())
	defer testutil.DrainAndClose(resp)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

var draftAttrRe = regexp.MustCompile(`data-draft="([^"]*)"`)

type reviewDraft struct {
	Parents []struct {
		Name     string `json:"parent_name"`
		StrainID *int   `json:"parent_strain_id"`
	} `json:"parents"`
	Provenance struct {
		Source        string `json:"source"`
		Key           string `json:"key"`
		LineageSource string `json:"lineage_source"`
	} `json:"provenance"`
}

func parseDraft(t *testing.T, page string) reviewDraft {
	t.Helper()
	m := draftAttrRe.FindStringSubmatch(page)
	require.NotNil(t, m, "review page carries the draft")
	var d reviewDraft
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(m[1])), &d))
	return d
}

func scReviewQuery() url.Values {
	return url.Values{"source": {"straincompass"}, "slug": {"blue-dream"}, "name": {"Blue Dream"}, "listing_breeder": {"Seed Supreme"}}
}

func TestImportReview_StraincompassPageIsPrefilledAndSavesNothing(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	status, page := s.reviewPage(t, scReviewQuery())
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `value="Blue Dream"`)
	assert.Contains(t, page, `data-initial-breeder="Seed Supreme"`)
	assert.Contains(t, page, `data-mode="review"`)

	d := parseDraft(t, page)
	assert.Equal(t, "straincompass", d.Provenance.Source)
	assert.Equal(t, "blue-dream", d.Provenance.Key)
	assert.Equal(t, "straincompass", d.Provenance.LineageSource)
	require.Len(t, d.Parents, 2)
	assert.Equal(t, "Blueberry", d.Parents[0].Name)
	assert.Equal(t, "Haze x Super Silver Haze", d.Parents[1].Name)

	assert.Equal(t, 0, s.count(t, "SELECT COUNT(*) FROM strain"), "nothing is saved before Save")
	assert.Equal(t, 0, s.count(t, "SELECT COUNT(*) FROM breeder"))
}

func TestImportReview_CannadbPage(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	status, page := s.reviewPage(t, url.Values{"source": {"cannadb"}, "uri": {fakeCannadbURI}})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `value="Runtz"`)
	assert.Contains(t, page, `data-initial-breeder="Cookies"`)
	d := parseDraft(t, page)
	assert.Equal(t, "cannadb", d.Provenance.Source)
	require.Len(t, d.Parents, 2)
	assert.Equal(t, "Zkittlez", d.Parents[0].Name)
}

func TestImportReview_StraincompassFallsBackToCannadbParents(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	_, page := s.reviewPage(t, url.Values{"source": {"straincompass"}, "slug": {"gelato"}, "name": {"Gelato"}, "listing_breeder": {"Cookies"}})
	d := parseDraft(t, page)
	assert.Equal(t, "cannadb", d.Provenance.LineageSource)
	require.Len(t, d.Parents, 2)
	assert.Equal(t, "Sunset Sherbet", d.Parents[0].Name)
}

func TestImportReview_ExistingStrainKeepsUserValues(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	breeder := testutil.SeedBreeder(t, s.db, "Seed Supreme")
	testutil.MustExec(t, s.db, `INSERT INTO strain (name, breeder_id, sativa, indica, autoflower, description, seed_count, seed_location, straincompass_slug)
		VALUES ('Blue Dream', $1, 50, 50, 1, 'old', 7, 'Jar 2', 'blue-dream')`, breeder)
	var id int
	require.NoError(t, s.db.QueryRow("SELECT id FROM strain WHERE straincompass_slug = 'blue-dream'").Scan(&id))
	testutil.MustExec(t, s.db, "INSERT INTO strain_lineage (strain_id, parent_name) VALUES ($1, 'My Own Parent')", id)

	_, page := s.reviewPage(t, scReviewQuery())
	assert.Contains(t, page, `href="/strain/`+strconv.Itoa(id)+`"`, "links the strain it will update")
	assert.Regexp(t, `id="editSeedCount"[^>]*value="7"`, page)
	assert.Regexp(t, `id="editSeedLocation"[^>]*value="Jar 2"`, page)
	assert.Regexp(t, `<option value="true" selected`, page, "StrainCompass has no autoflower flag, so the user's is kept")
	d := parseDraft(t, page)
	require.Len(t, d.Parents, 1)
	assert.Equal(t, "My Own Parent", d.Parents[0].Name, "the user's own parents win over the source's")
	assert.Empty(t, d.Provenance.LineageSource)
}

func TestImportReview_Errors(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	s.server.ConfigStore.SetStraincompassEnabled(0)

	_, page := s.reviewPage(t, scReviewQuery())
	assert.Contains(t, page, "alert-danger")
	assert.NotContains(t, page, `id="editStrainForm"`)

	_, page = s.reviewPage(t, url.Values{"source": {"somewhere"}})
	assert.Contains(t, page, "alert-danger")
}

func reviewSavePayload(overrides map[string]any) map[string]any {
	p := map[string]any{
		"name": "Blue Dream (mine)", "breeder_id": nil, "new_breeder": "Seed Supreme",
		"indica": 40, "sativa": 60, "autoflower": false, "seed_count": 5, "seed_location": "Jar 1",
		"description": "desc", "short_desc": "short", "cycle_time": 63, "url": "",
		"growing":      map[string]any{"height_indoor": "90-150cm"},
		"cannabinoids": map[string]any{"thc_max": 24},
		"attributes":   map[string]any{"effects": []map[string]any{{"name": "Happy"}}, "flavors": []any{}, "terpenes": []any{}, "medical_uses": []any{}},
		"parents":      []map[string]any{{"parent_name": "Blueberry"}},
		"provenance":   map[string]any{"source": "straincompass", "key": "blue-dream", "lineage_source": "straincompass", "lineage_note": "Blueberry x Haze"},
		"keep_lineage_source": true,
		"packaging":           map[string]any{"choice": "none"},
	}
	for k, v := range overrides {
		p[k] = v
	}
	return p
}

type saveResult struct {
	ID      int  `json:"id"`
	Updated bool `json:"updated"`
}

func (s *importServer) save(t *testing.T, payload map[string]any) (int, saveResult) {
	t.Helper()
	resp := s.client.SessionPostJSON(t, "/strains/import/save", s.token, payload)
	defer testutil.DrainAndClose(resp)
	var got saveResult
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	}
	return resp.StatusCode, got
}

func TestImportSave_AddsThenUpdates(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	status, first := s.save(t, reviewSavePayload(nil))
	require.Equal(t, http.StatusOK, status)
	assert.False(t, first.Updated)

	var name, slug, source, loc, note, height sql.NullString
	var thc sql.NullFloat64
	require.NoError(t, s.db.QueryRow(`SELECT name, straincompass_slug, lineage_source, seed_location, straincompass_lineage_note, height_indoor, thc_max
		FROM strain WHERE id = $1`, first.ID).Scan(&name, &slug, &source, &loc, &note, &height, &thc))
	assert.Equal(t, "Blue Dream (mine)", name.String, "the user's edits are what gets saved")
	assert.Equal(t, "blue-dream", slug.String)
	assert.Equal(t, "straincompass", source.String)
	assert.Equal(t, "Jar 1", loc.String)
	assert.Equal(t, "Blueberry x Haze", note.String)
	assert.Equal(t, "90-150cm", height.String)
	assert.Equal(t, 24.0, thc.Float64)
	assert.Equal(t, 1, s.count(t, "SELECT COUNT(*) FROM strain_effect WHERE strain_id = $1 AND name = 'Happy'", first.ID))
	assert.Equal(t, 1, s.count(t, "SELECT COUNT(*) FROM strain_lineage WHERE strain_id = $1 AND parent_name = 'Blueberry'", first.ID))

	// Same listing, same breeder: updates in place.
	status, second := s.save(t, reviewSavePayload(map[string]any{"name": "Blue Dream", "keep_lineage_source": false,
		"parents": []map[string]any{{"parent_name": "Blueberry"}, {"parent_name": "Haze"}}}))
	require.Equal(t, http.StatusOK, status)
	assert.True(t, second.Updated)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, 1, s.count(t, "SELECT COUNT(*) FROM strain"))
	assert.Equal(t, 2, s.count(t, "SELECT COUNT(*) FROM strain_lineage WHERE strain_id = $1", first.ID))
	require.NoError(t, s.db.QueryRow("SELECT lineage_source FROM strain WHERE id = $1", first.ID).Scan(&source))
	assert.False(t, source.Valid, "edited parents are the user's own")

	// Same listing, another breeder: a separate strain.
	status, third := s.save(t, reviewSavePayload(map[string]any{"new_breeder": "Humboldt Seed Co"}))
	require.Equal(t, http.StatusOK, status)
	assert.False(t, third.Updated)
	assert.NotEqual(t, first.ID, third.ID)
}

func TestImportSave_KeepsHeldSeedPackImage(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	pending := filepath.Join(s.server.UploadDir, "strains", "pending")
	require.NoError(t, os.MkdirAll(pending, 0o755))
	token := "0123456789abcdef0123456789abcdef"
	draft := filepath.Join(pending, "draft_"+token+".png")
	require.NoError(t, os.WriteFile(draft, pngBytes(t), 0o644))

	status, got := s.save(t, reviewSavePayload(map[string]any{"packaging": map[string]any{"choice": "keep", "token": token}}))
	require.Equal(t, http.StatusOK, status)
	var img sql.NullString
	require.NoError(t, s.db.QueryRow("SELECT packaging_image FROM strain WHERE id = $1", got.ID).Scan(&img))
	assert.NotEmpty(t, img.String)
	assert.FileExists(t, img.String)
	assert.NoFileExists(t, draft)
}

func TestImportSave_NoImageDropsHeldCopy(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	pending := filepath.Join(s.server.UploadDir, "strains", "pending")
	require.NoError(t, os.MkdirAll(pending, 0o755))
	token := "fedcba9876543210fedcba9876543210"
	draft := filepath.Join(pending, "draft_"+token+".png")
	require.NoError(t, os.WriteFile(draft, pngBytes(t), 0o644))

	status, got := s.save(t, reviewSavePayload(map[string]any{"packaging": map[string]any{"choice": "none", "token": token}}))
	require.Equal(t, http.StatusOK, status)
	assert.NoFileExists(t, draft)
	var img sql.NullString
	require.NoError(t, s.db.QueryRow("SELECT packaging_image FROM strain WHERE id = $1", got.ID).Scan(&img))
	assert.False(t, img.Valid)
}

func TestImportSave_RejectsBadInput(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)

	for name, payload := range map[string]map[string]any{
		"unknown source":     reviewSavePayload(map[string]any{"provenance": map[string]any{"source": "elsewhere", "key": "x"}}),
		"missing key":        reviewSavePayload(map[string]any{"provenance": map[string]any{"source": "straincompass"}}),
		"cannadb key format": reviewSavePayload(map[string]any{"provenance": map[string]any{"source": "cannadb", "key": "not-an-at-uri"}}),
		"ratio":              reviewSavePayload(map[string]any{"indica": 70}),
		"no breeder":         reviewSavePayload(map[string]any{"new_breeder": ""}),
		"cannabinoids":       reviewSavePayload(map[string]any{"cannabinoids": map[string]any{"thc_min": 30, "thc_max": 20}}),
	} {
		status, _ := s.save(t, payload)
		assert.Equal(t, http.StatusBadRequest, status, name)
	}
	assert.Equal(t, 0, s.count(t, "SELECT COUNT(*) FROM strain"))
}

func TestImportReview_RequiresLogin(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	c := server.NewClient(t)

	resp := c.Get("/strain/import/review?" + scReviewQuery().Encode())
	testutil.DrainAndClose(resp)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "page must not render logged out")

	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/strains/import/save", nil)
	require.NoError(t, err)
	resp, err = c.Do(req)
	require.NoError(t, err)
	testutil.DrainAndClose(resp)
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, resp.StatusCode)
}
