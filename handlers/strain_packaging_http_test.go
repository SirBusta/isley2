package handlers_test

// Seed-pack image endpoints: upload/replace/remove, and accepting or
// discarding an image held from CannaDB.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

const packagingPassword = "packaging-pw"

type packagingEnv struct {
	db       *sql.DB
	server   *testutil.TestServer
	client   *testutil.Client
	token    string
	strainID int
	base     string
}

func newPackagingEnv(t *testing.T) *packagingEnv {
	t.Helper()
	db := testutil.NewTestDB(t)
	// Own upload dir: every test DB's strain is id 1, so a shared dir would collide.
	server := testutil.NewTestServer(t, db, testutil.WithUploadDir(t.TempDir()))
	testutil.SeedAdmin(t, db, packagingPassword)
	breeder := testutil.SeedBreeder(t, db, "Pack Breeder")
	strainID := testutil.SeedStrain(t, db, breeder, "Pack Strain")
	c, token := server.LoginAndFetchCSRF(t, packagingPassword, "/strain/"+strconv.Itoa(strainID)+"/edit")
	return &packagingEnv{db: db, server: server, client: c, token: token, strainID: strainID,
		base: "/strains/" + strconv.Itoa(strainID) + "/packaging-image"}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 3))))
	return buf.Bytes()
}

func (e *packagingEnv) do(t *testing.T, method, path string, body *bytes.Buffer, contentType string) (int, string) {
	t.Helper()
	if body == nil {
		body = &bytes.Buffer{}
	}
	req, err := http.NewRequest(method, e.client.BaseURL+path, body)
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-CSRF-Token", e.token)
	resp, err := e.client.Do(req)
	require.NoError(t, err)
	defer testutil.DrainAndClose(resp)
	var out struct {
		PackagingImage string `json:"packaging_image"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.PackagingImage
}

func (e *packagingEnv) upload(t *testing.T, name string, payload []byte) (int, string) {
	t.Helper()
	body, ct := testutil.MultipartBody(t, "image", name, payload)
	return e.do(t, http.MethodPost, e.base, body, ct)
}

func (e *packagingEnv) stored(t *testing.T) string {
	t.Helper()
	var p sql.NullString
	require.NoError(t, e.db.QueryRow("SELECT packaging_image FROM strain WHERE id = $1", e.strainID).Scan(&p))
	return p.String
}

func TestPackagingImage_UploadReplaceRemove(t *testing.T) {
	t.Parallel()
	e := newPackagingEnv(t)

	status, url := e.upload(t, "pack.png", pngBytes(t))
	require.Equal(t, http.StatusOK, status)
	first := e.stored(t)
	require.NotEmpty(t, first)
	assert.Contains(t, url, "/strains/strain_")
	assert.FileExists(t, first)

	status, _ = e.upload(t, "pack2.png", pngBytes(t))
	require.Equal(t, http.StatusOK, status)
	second := e.stored(t)
	assert.NotEqual(t, first, second)
	assert.NoFileExists(t, first, "the replaced file is deleted")
	assert.FileExists(t, second)

	status, url = e.do(t, http.MethodDelete, e.base, nil, "")
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, url)
	assert.Empty(t, e.stored(t))
	assert.NoFileExists(t, second)
}

func TestPackagingImage_RejectsNonImage(t *testing.T) {
	t.Parallel()
	e := newPackagingEnv(t)
	status, _ := e.upload(t, "pack.png", []byte("definitely not an image"))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Empty(t, e.stored(t))
}

func TestPackagingImage_AcceptAndDiscardHeldOffer(t *testing.T) {
	t.Parallel()
	e := newPackagingEnv(t)
	pendingDir := filepath.Join(e.server.UploadDir, "strains", "pending")
	require.NoError(t, os.MkdirAll(pendingDir, 0o755))
	pending := filepath.Join(pendingDir, "strain_"+strconv.Itoa(e.strainID)+".png")

	// Nothing held yet.
	status, _ := e.do(t, http.MethodPost, e.base+"/accept", nil, "")
	assert.Equal(t, http.StatusNotFound, status)

	// "Leave blank" drops the held file.
	require.NoError(t, os.WriteFile(pending, pngBytes(t), 0o644))
	status, _ = e.do(t, http.MethodPost, e.base+"/discard", nil, "")
	require.Equal(t, http.StatusOK, status)
	assert.NoFileExists(t, pending)
	assert.Empty(t, e.stored(t))

	// "Import" keeps it.
	require.NoError(t, os.WriteFile(pending, pngBytes(t), 0o644))
	status, url := e.do(t, http.MethodPost, e.base+"/accept", nil, "")
	require.Equal(t, http.StatusOK, status)
	assert.NotEmpty(t, url)
	assert.NoFileExists(t, pending)
	assert.FileExists(t, e.stored(t))
}

func TestPackagingImage_UnknownStrain(t *testing.T) {
	t.Parallel()
	e := newPackagingEnv(t)
	status, _ := e.do(t, http.MethodDelete, "/strains/99999/packaging-image", nil, "")
	assert.Equal(t, http.StatusNotFound, status)
}

func TestPackagingImage_DeletingStrainRemovesFile(t *testing.T) {
	t.Parallel()
	e := newPackagingEnv(t)
	status, _ := e.upload(t, "pack.png", pngBytes(t))
	require.Equal(t, http.StatusOK, status)
	file := e.stored(t)

	status, _ = e.do(t, http.MethodDelete, "/strains/"+strconv.Itoa(e.strainID), nil, "")
	require.Equal(t, http.StatusOK, status)
	assert.NoFileExists(t, file)
}
