package handlers_test

// Flowering time / seed-to-harvest as a range ("8–10 weeks"): cycle_time is
// the long end (harvest estimate, sorting), cycle_time_min the short end.

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (e *strainEditEnv) cycle(t *testing.T) (int, sql.NullInt64) {
	t.Helper()
	var max int
	var min sql.NullInt64
	require.NoError(t, e.db.QueryRow("SELECT cycle_time, cycle_time_min FROM strain WHERE id = $1", e.strainID).Scan(&max, &min))
	return max, min
}

func TestStrainCycleRange_Edit(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"cycle_time": 70, "cycle_time_min": 56}))
	max, min := e.cycle(t)
	assert.Equal(t, 70, max)
	assert.Equal(t, int64(56), min.Int64)

	page := e.getBody(t, "/strain/"+strconv.Itoa(e.strainID))
	assert.Contains(t, page, "8–10 weeks")
	edit := e.getBody(t, "/strain/"+strconv.Itoa(e.strainID)+"/edit")
	assert.Regexp(t, `id="editCycleTimeMin"[^>]*value="8"`, edit)
	assert.Regexp(t, `id="editCycleTime"[^>]*value="10"`, edit)

	// Not sent: kept, unless the new long end no longer exceeds it.
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"cycle_time": 63}))
	_, min = e.cycle(t)
	assert.Equal(t, int64(56), min.Int64)
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"cycle_time": 56}))
	_, min = e.cycle(t)
	assert.False(t, min.Valid, "8 weeks is no longer a range when the long end is 8 weeks too")

	assert.Equal(t, http.StatusBadRequest, e.put(t, map[string]any{"cycle_time": 56, "cycle_time_min": 70}), "short end longer than long end")

	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"cycle_time": 63, "cycle_time_min": 63}))
	_, min = e.cycle(t)
	assert.False(t, min.Valid, "equal ends are a single value")
	assert.Contains(t, e.getBody(t, "/strain/"+strconv.Itoa(e.strainID)), "9 weeks")
}

// Browsers refuse to submit a number box whose value doesn't fit its step,
// silently blocking Save: 60 days shows as 8.6 weeks and StrainCompass sends
// THC like 27.95, so these boxes must accept any decimal.
func TestStrainEditPage_DecimalValuesFitTheirBoxes(t *testing.T) {
	t.Parallel()
	e := newStrainEditEnv(t)
	require.Equal(t, http.StatusOK, e.put(t, map[string]any{"cycle_time": 60, "cycle_time_min": 53,
		"cannabinoids": map[string]any{"thc_max": 27.95}}))

	page := e.getBody(t, "/strain/"+strconv.Itoa(e.strainID)+"/edit")
	assert.Regexp(t, `id="editCycleTime" min="0" step="any" value="8.6"`, page)
	assert.Regexp(t, `id="editCycleTimeMin" min="0" step="any" value="7.6"`, page)
	assert.Regexp(t, `id="editThcMax" min="0" max="100" step="any"[^>]*value="27.95"`, page)
	assert.NotRegexp(t, `id="edit(CycleTime|CycleTimeMin|Thc|Cbd|Cbn|Cbg)[A-Za-z]*"[^>]*step="0\.`, page)
}

func TestStrainCycleRange_ImportReviewAndSave(t *testing.T) {
	t.Parallel()
	s := newImportServer(t)
	// The fake Haze Automatic listing has only floweringTimeMax (11 weeks).
	_, page := s.reviewPage(t, url.Values{"source": {"straincompass"}, "slug": {"haze-automatic"},
		"name": {"Haze Automatic"}, "listing_breeder": {"Dinafem Seeds"}})
	assert.Regexp(t, `id="editCycleTimeMin"[^>]*value=""`, page)
	assert.Regexp(t, `id="editCycleTime"[^>]*value="11"`, page)

	status, saved := s.save(t, reviewSavePayload(map[string]any{"cycle_time": 70, "cycle_time_min": 56}))
	require.Equal(t, http.StatusOK, status)
	var min sql.NullInt64
	require.NoError(t, s.db.QueryRow("SELECT cycle_time_min FROM strain WHERE id = $1", saved.ID).Scan(&min))
	assert.Equal(t, int64(56), min.Int64)

	status, _ = s.save(t, reviewSavePayload(map[string]any{"cycle_time": 56, "cycle_time_min": 70}))
	assert.Equal(t, http.StatusBadRequest, status)
}
