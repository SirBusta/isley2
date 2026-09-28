package handlers_test

// Monthly breeder reference refresh against a fake StrainCompass /breeders
// page: stores a sane list, sends If-None-Match afterwards, and keeps the
// previous list when a fetch fails or comes back suspiciously short.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/handlers"
	"isley/tests/testutil"
)

func breederDirectoryPage(n int) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<a href="/breeders/%s">x</a>`, url.PathEscape(fmt.Sprintf("Test Breeder %03d", i)))
	}
	b.WriteString("</body></html>")
	return b.String()
}

type fakeDirectory struct {
	mu          sync.Mutex
	names       int
	status      int
	etag        string
	gotIfNone   string
	gotAgent    string
	requestSeen int
}

func (f *fakeDirectory) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requestSeen++
		f.gotIfNone = r.Header.Get("If-None-Match")
		f.gotAgent = r.Header.Get("User-Agent")
		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		if f.etag != "" && f.gotIfNone == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if f.etag != "" {
			w.Header().Set("ETag", f.etag)
		}
		_, _ = w.Write([]byte(breederDirectoryPage(f.names)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBreederReferenceRefresh_StoresListAndUsesETag(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	assets := testutil.RepoFS(t)
	fake := &fakeDirectory{names: 1200, etag: `"v1"`}
	srv := fake.server(t)

	res, err := handlers.RefreshBreederReference(context.Background(), db, assets, srv.URL+"/breeders")
	require.NoError(t, err)
	assert.True(t, res.Updated)
	assert.Equal(t, 1200, res.Count)
	assert.True(t, strings.HasPrefix(fake.gotAgent, "Isley/"), "honest User-Agent, got %q", fake.gotAgent)
	assert.Empty(t, fake.gotIfNone, "first fetch is unconditional")

	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM breeder_reference").Scan(&n))
	assert.Equal(t, 1200, n)

	// Second refresh is conditional and a 304 leaves the list alone.
	res, err = handlers.RefreshBreederReference(context.Background(), db, assets, srv.URL+"/breeders")
	require.NoError(t, err)
	assert.False(t, res.Updated)
	assert.Equal(t, `"v1"`, fake.gotIfNone)
	assert.Equal(t, 1200, res.Count)
}

func TestBreederReferenceRefresh_RejectsCollapsedList(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	assets := testutil.RepoFS(t)
	fake := &fakeDirectory{names: 1200}
	srv := fake.server(t)

	_, err := handlers.RefreshBreederReference(context.Background(), db, assets, srv.URL+"/breeders")
	require.NoError(t, err)

	fake.mu.Lock()
	fake.names = 500 // less than half of 1200
	fake.mu.Unlock()
	res, err := handlers.RefreshBreederReference(context.Background(), db, assets, srv.URL+"/breeders")
	require.Error(t, err)
	assert.False(t, res.Updated)

	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM breeder_reference").Scan(&n))
	assert.Equal(t, 1200, n, "previous list kept")

	var lastErr string
	require.NoError(t, db.QueryRow("SELECT value FROM settings WHERE name = 'breeder_reference.last_error'").Scan(&lastErr))
	assert.Contains(t, lastErr, "500 names")
}

func TestBreederReferenceRefresh_RejectsListSmallerThanHalfTheBundle(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	fake := &fakeDirectory{names: 300} // bundled snapshot has ~1,172
	srv := fake.server(t)

	_, err := handlers.RefreshBreederReference(context.Background(), db, testutil.RepoFS(t), srv.URL+"/breeders")
	require.Error(t, err)

	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM breeder_reference").Scan(&n))
	assert.Equal(t, 0, n, "nothing stored; bundled list stays in use")
}

func TestBreederReferenceRefresh_HTTPErrorKeepsList(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	fake := &fakeDirectory{status: http.StatusServiceUnavailable}
	srv := fake.server(t)

	_, err := handlers.RefreshBreederReference(context.Background(), db, testutil.RepoFS(t), srv.URL+"/breeders")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestBreederReferenceHTTP_StatusSettingsAndRefreshedList(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	testutil.SeedAdmin(t, db, breederDedupePassword)
	c, token := server.LoginAndFetchCSRF(t, breederDedupePassword, "/settings")

	getStatus := func() handlers.BreederReferenceStatus {
		resp := c.Get("/breeders/reference/status")
		defer testutil.DrainAndClose(resp)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var s handlers.BreederReferenceStatus
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))
		return s
	}

	s := getStatus()
	assert.True(t, s.AutoRefresh, "auto refresh is on unless turned off")
	assert.Equal(t, "bundled", s.Source)
	assert.Greater(t, s.Count, 1000)

	resp := c.SessionPostJSON(t, "/breeders/reference/settings", token, map[string]bool{"auto_refresh": false})
	testutil.DrainAndClose(resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.False(t, getStatus().AutoRefresh)

	// Once a refreshed list is stored, suggestions come from it.
	fake := &fakeDirectory{names: 1200}
	srv := fake.server(t)
	_, err := handlers.RefreshBreederReference(context.Background(), db, testutil.RepoFS(t), srv.URL+"/breeders")
	require.NoError(t, err)

	s = getStatus()
	assert.Equal(t, "refreshed", s.Source)
	assert.Equal(t, 1200, s.Count)
	assert.NotEmpty(t, s.LastUpdated)

	resp = c.Get("/breeders/reference")
	defer testutil.DrainAndClose(resp)
	var got struct {
		Names []string `json:"names"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Names, 1200)
	assert.Equal(t, "Test Breeder 000", got.Names[0])
}

func TestBreederReferenceHTTP_RequiresLogin(t *testing.T) {
	t.Parallel()
	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)
	c := server.NewClient(t)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/breeders/reference/status"},
		{http.MethodPost, "/breeders/reference/settings"},
		{http.MethodPost, "/breeders/reference/refresh"},
	} {
		req, err := http.NewRequest(tc.method, c.BaseURL+tc.path, nil)
		require.NoError(t, err)
		resp, err := c.Do(req)
		require.NoError(t, err)
		testutil.DrainAndClose(resp)
		assert.Containsf(t, []int{http.StatusUnauthorized, http.StatusForbidden}, resp.StatusCode, "%s %s", tc.method, tc.path)
	}
}
