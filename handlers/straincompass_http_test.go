package handlers_test

// HTTP-layer tests for handlers/straincompass.go (StrainCompass import).
//
// The success paths require live calls to the StrainCompass API, which tests
// must not make, so this file covers auth gating, the integration-disabled
// branch (the default), and basic request validation.
//
// Routes (both api-protected):
//   GET  /strains/straincompass/search  → StraincompassSearchHandler
//   POST /strains/straincompass/import  → StraincompassImportHandler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

func TestStraincompassHTTP_AuthGating(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/strains/straincompass/search?q=og"},
		{http.MethodPost, "/strains/straincompass/import"},
	}

	c := server.NewClient(t)
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, c.BaseURL+tc.path, nil)
			require.NoError(t, err)
			resp, err := c.Do(req)
			require.NoError(t, err)
			defer testutil.DrainAndClose(resp)
			assert.Containsf(t,
				[]int{http.StatusUnauthorized, http.StatusForbidden},
				resp.StatusCode,
				"%s %s should be rejected (got %d)", tc.method, tc.path, resp.StatusCode)
		})
	}
}

// With the integration disabled (the default — no straincompass.enabled
// setting), authenticated requests are rejected with 400 before any
// outbound call.
func TestStraincompassHTTP_DisabledByDefault(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	server := testutil.NewTestServer(t, db)

	const apiKey = "straincompass-disabled-key"
	testutil.SeedAPIKey(t, db, apiKey)

	c := server.NewClient(t)

	t.Run("search", func(t *testing.T) {
		resp, err := c.Do(testutil.APIReq(t, http.MethodGet, c.BaseURL+"/strains/straincompass/search?q=og", apiKey, nil, ""))
		require.NoError(t, err)
		defer testutil.DrainAndClose(resp)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("import", func(t *testing.T) {
		resp, err := c.Do(testutil.APIReq(t, http.MethodPost, c.BaseURL+"/strains/straincompass/import", apiKey,
			testutil.JSONBody(t, map[string]interface{}{"slug": "permanent-marker"}), "application/json"))
		require.NoError(t, err)
		defer testutil.DrainAndClose(resp)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}
