package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register decoders for image validation
	_ "image/jpeg" //
	_ "image/png"  //
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "golang.org/x/image/webp" // CannaDB serves webp

	"isley/logger"

	"github.com/gin-gonic/gin"
)

// Seed-pack (packaging) images. When an import finds one on CannaDB, Isley
// downloads it once into a holding spot and the UI asks the user to keep it,
// upload their own, or leave it blank. Images are always stored locally.

const (
	maxPackagingImageSize = 5 << 20
	plcDirectoryURL       = "https://plc.directory/"
)

var allowedPackagingMIME = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// isPublicIP reports whether ip is routable on the public internet.
func isPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64 { // 100.64.0.0/10 carrier-grade NAT
		return false
	}
	return true
}

// publicOnlyClient refuses to connect anywhere but public addresses. The
// check runs on the resolved IP at dial time, so a hostname in a DID
// document can't be pointed at the user's LAN.
var publicOnlyClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if ip := net.ParseIP(host); ip == nil || !isPublicIP(ip) {
					return fmt.Errorf("refusing to connect to non-public address %s", host)
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" {
			return errors.New("refusing redirect")
		}
		return nil
	},
}

// atprotoBlobFetcher downloads blobs from a record author's PDS. plcBase and
// client are fields so tests can point them at local fakes.
type atprotoBlobFetcher struct {
	client  *http.Client
	plcBase string
}

var defaultBlobFetcher = atprotoBlobFetcher{client: publicOnlyClient, plcBase: plcDirectoryURL}

func (f atprotoBlobFetcher) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", straincompassUserAgent())
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", req.URL.Host, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response too large")
	}
	return body, nil
}

// pdsFor resolves a did:plc identity to its PDS base URL (https only).
func (f atprotoBlobFetcher) pdsFor(ctx context.Context, did string) (string, error) {
	if !strings.HasPrefix(did, "did:plc:") || strings.ContainsAny(did, "/?#") {
		return "", fmt.Errorf("unsupported DID %q", did)
	}
	body, err := f.get(ctx, f.plcBase+did, 64<<10)
	if err != nil {
		return "", err
	}
	var doc struct {
		Service []struct {
			Type            string `json:"type"`
			ServiceEndpoint string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", err
	}
	for _, s := range doc.Service {
		if s.Type == "AtprotoPersonalDataServer" {
			u, err := url.Parse(s.ServiceEndpoint)
			if err != nil || (u.Scheme != "https" && f.client == publicOnlyClient) || u.Host == "" {
				return "", fmt.Errorf("unusable PDS endpoint %q", s.ServiceEndpoint)
			}
			return strings.TrimRight(s.ServiceEndpoint, "/"), nil
		}
	}
	return "", errors.New("no PDS in DID document")
}

// fetchBlob downloads one blob and checks that it is an image Isley accepts.
// It returns the bytes and the file extension to store them under.
func (f atprotoBlobFetcher) fetchBlob(ctx context.Context, did, cid string) ([]byte, string, error) {
	pds, err := f.pdsFor(ctx, did)
	if err != nil {
		return nil, "", err
	}
	u := pds + "/xrpc/com.atproto.sync.getBlob?did=" + url.QueryEscape(did) + "&cid=" + url.QueryEscape(cid)
	body, err := f.get(ctx, u, maxPackagingImageSize)
	if err != nil {
		return nil, "", err
	}
	ext, err := validateImageBytes(body)
	return body, ext, err
}

// validateImageBytes checks the content is a JPEG/PNG/GIF/WebP that decodes,
// returning its file extension.
func validateImageBytes(b []byte) (string, error) {
	ext, ok := allowedPackagingMIME[http.DetectContentType(b)]
	if !ok {
		return "", errors.New("api_invalid_file_type")
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(b)); err != nil {
		return "", errors.New("api_invalid_file_type")
	}
	return ext, nil
}

// didFromATURI returns the DID in "at://<did>/collection/rkey".
func didFromATURI(uri string) string {
	rest := strings.TrimPrefix(uri, "at://")
	if rest == uri {
		return ""
	}
	if i := strings.Index(rest, "/"); i > 0 {
		return rest[:i]
	}
	return rest
}

func strainsUploadDir(uploadDir string) string  { return filepath.Join(uploadDir, "strains") }
func pendingPackagingDir(uploadDir string) string { return filepath.Join(uploadDir, "strains", "pending") }

// pendingPackagingPath returns the held (not yet accepted) image for a
// strain, or "" when there is none.
func pendingPackagingPath(uploadDir string, strainID int) string {
	matches, _ := filepath.Glob(filepath.Join(pendingPackagingDir(uploadDir), fmt.Sprintf("strain_%d.*", strainID)))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// PendingPackagingURL is the URL of a held CannaDB image still waiting for
// the user's decision on the strain in c's ":id" param ("" when none).
func PendingPackagingURL(c *gin.Context) string {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return ""
	}
	if p := pendingPackagingPath(UploadDirFromContext(c), id); p != "" {
		return webPath(p)
	}
	return ""
}

func removePendingPackaging(uploadDir string, strainID int) {
	if p := pendingPackagingPath(uploadDir, strainID); p != "" {
		_ = os.Remove(p)
	}
}

// webPath turns a stored file path into the URL the browser loads.
func webPath(p string) string {
	return "/" + strings.TrimPrefix(filepath.ToSlash(p), "/")
}

// downloadCannadbPackagingImage fetches a CannaDB record's seed-pack image
// (its primaryImage blob), if it has one.
func downloadCannadbPackagingImage(ctx context.Context, rec *cannadbRecord, val *cannadbStrainValue) ([]byte, string, bool) {
	if rec == nil || val == nil || val.PrimaryImage == nil || val.PrimaryImage.Ref.Link == "" {
		return nil, "", false
	}
	did := rec.DID
	if did == "" {
		did = didFromATURI(rec.URI)
	}
	body, ext, err := defaultBlobFetcher.fetchBlob(ctx, did, val.PrimaryImage.Ref.Link)
	if err != nil {
		logger.Log.WithField("func", "downloadCannadbPackagingImage").WithError(err).Warn("Could not fetch CannaDB seed-pack image")
		return nil, "", false
	}
	return body, ext, true
}

// Draft images belong to a review page that hasn't been saved yet; they are
// keyed by a random token instead of a strain id.
var draftTokenRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

const draftPackagingMaxAge = 24 * time.Hour

func draftPackagingPath(uploadDir, token string) string {
	if !draftTokenRe.MatchString(token) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(pendingPackagingDir(uploadDir), "draft_"+token+".*"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func removeDraftPackaging(uploadDir, token string) {
	if p := draftPackagingPath(uploadDir, token); p != "" {
		_ = os.Remove(p)
	}
}

// cleanupStaleDraftPackaging deletes draft images from review pages that
// were abandoned more than a day ago.
func cleanupStaleDraftPackaging(uploadDir string) {
	matches, _ := filepath.Glob(filepath.Join(pendingPackagingDir(uploadDir), "draft_*"))
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && time.Since(info.ModTime()) > draftPackagingMaxAge {
			_ = os.Remove(m)
		}
	}
}

// stageDraftPackagingImage saves image bytes for an unsaved review page and
// returns its token and URL.
func stageDraftPackagingImage(uploadDir string, body []byte, ext string) (token, url string, err error) {
	cleanupStaleDraftPackaging(uploadDir)
	buf := make([]byte, 16)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)
	if err = os.MkdirAll(pendingPackagingDir(uploadDir), 0o755); err != nil {
		return "", "", err
	}
	p := filepath.Join(pendingPackagingDir(uploadDir), "draft_"+token+ext)
	if err = os.WriteFile(p, body, 0o644); err != nil {
		return "", "", err
	}
	return token, webPath(p), nil
}

// adoptDraftPackagingImage makes a draft image the strain's packaging image.
func adoptDraftPackagingImage(db *sql.DB, uploadDir, token string, strainID int) error {
	draft := draftPackagingPath(uploadDir, token)
	if draft == "" {
		return errors.New("draft image not found")
	}
	final := filepath.Join(strainsUploadDir(uploadDir),
		fmt.Sprintf("strain_%d_packaging_%d%s", strainID, time.Now().UnixNano(), filepath.Ext(draft)))
	if err := os.Rename(draft, final); err != nil {
		return err
	}
	if err := setPackagingImage(db, uploadDir, strainID, final); err != nil {
		_ = os.Remove(final)
		return err
	}
	return nil
}

// offerCannadbPackagingImage downloads the CannaDB record's seed-pack image
// into the holding spot when the strain has no packaging image yet, and
// returns its URL for the "keep it?" prompt ("" when nothing is offered).
func offerCannadbPackagingImage(ctx context.Context, db *sql.DB, uploadDir string, strainID int, rec *cannadbRecord, val *cannadbStrainValue) string {
	if rec == nil || val == nil || val.PrimaryImage == nil || val.PrimaryImage.Ref.Link == "" {
		return ""
	}
	var current sql.NullString
	if err := db.QueryRow("SELECT packaging_image FROM strain WHERE id = $1", strainID).Scan(&current); err != nil || current.String != "" {
		return ""
	}
	body, ext, ok := downloadCannadbPackagingImage(ctx, rec, val)
	if !ok {
		return ""
	}
	if err := os.MkdirAll(pendingPackagingDir(uploadDir), 0o755); err != nil {
		return ""
	}
	removePendingPackaging(uploadDir, strainID)
	p := filepath.Join(pendingPackagingDir(uploadDir), fmt.Sprintf("strain_%d%s", strainID, ext))
	if err := os.WriteFile(p, body, 0o644); err != nil {
		return ""
	}
	return webPath(p)
}

// setPackagingImage stores newPath as the strain's packaging image and
// deletes the file it replaces (only if it lies inside the strains folder).
func setPackagingImage(db *sql.DB, uploadDir string, strainID int, newPath string) error {
	var old sql.NullString
	if err := db.QueryRow("SELECT packaging_image FROM strain WHERE id = $1", strainID).Scan(&old); err != nil {
		return err
	}
	var stored any
	if newPath != "" {
		stored = filepath.ToSlash(newPath)
	}
	if _, err := db.Exec("UPDATE strain SET packaging_image = $1 WHERE id = $2", stored, strainID); err != nil {
		return err
	}
	removeStrainImageFile(uploadDir, old.String)
	return nil
}

// removeStrainImageFile deletes a stored strain image, refusing any path
// outside uploads/strains.
func removeStrainImageFile(uploadDir, stored string) {
	if stored == "" {
		return
	}
	root, err1 := filepath.Abs(strainsUploadDir(uploadDir))
	target, err2 := filepath.Abs(filepath.FromSlash(stored))
	if err1 != nil || err2 != nil || !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return
	}
	_ = os.Remove(target)
}

func packagingStrainID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apiBadRequest(c, "api_invalid_strain_id")
		return 0, false
	}
	var exists int
	if err := DBFromContext(c).QueryRow("SELECT COUNT(*) FROM strain WHERE id = $1", id).Scan(&exists); err != nil || exists == 0 {
		apiNotFound(c, "api_strain_not_found")
		return 0, false
	}
	return id, true
}

func packagingResponse(c *gin.Context, strainID int, msgKey string) {
	var p sql.NullString
	_ = DBFromContext(c).QueryRow("SELECT packaging_image FROM strain WHERE id = $1", strainID).Scan(&p)
	url := ""
	if p.String != "" {
		url = webPath(p.String)
	}
	c.JSON(http.StatusOK, gin.H{"packaging_image": url, "message": T(c, msgKey)})
}

// AcceptPackagingImageHandler keeps the held CannaDB image.
// POST /strains/:id/packaging-image/accept
func AcceptPackagingImageHandler(c *gin.Context) {
	strainID, ok := packagingStrainID(c)
	if !ok {
		return
	}
	uploadDir := UploadDirFromContext(c)
	pending := pendingPackagingPath(uploadDir, strainID)
	if pending == "" {
		apiNotFound(c, "api_packaging_no_offer")
		return
	}
	final := filepath.Join(strainsUploadDir(uploadDir),
		fmt.Sprintf("strain_%d_packaging_%d%s", strainID, time.Now().UnixNano(), filepath.Ext(pending)))
	if err := os.Rename(pending, final); err != nil {
		apiInternalError(c, "api_failed_to_save_file")
		return
	}
	if err := setPackagingImage(DBFromContext(c), uploadDir, strainID, final); err != nil {
		_ = os.Remove(final)
		apiInternalError(c, "api_failed_to_save_file")
		return
	}
	packagingResponse(c, strainID, "api_packaging_saved")
}

// DiscardPackagingImageHandler drops the held CannaDB image ("leave blank").
// POST /strains/:id/packaging-image/discard
func DiscardPackagingImageHandler(c *gin.Context) {
	strainID, ok := packagingStrainID(c)
	if !ok {
		return
	}
	removePendingPackaging(UploadDirFromContext(c), strainID)
	packagingResponse(c, strainID, "api_packaging_skipped")
}

// UploadPackagingImageHandler stores a user-supplied image (multipart field
// "image"), replacing any existing one and any held offer.
// POST /strains/:id/packaging-image
func UploadPackagingImageHandler(c *gin.Context) {
	strainID, ok := packagingStrainID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPackagingImageSize+(1<<20))
	fh, err := c.FormFile("image")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			apiError(c, http.StatusRequestEntityTooLarge, "api_image_too_large")
		} else {
			apiBadRequest(c, "api_failed_to_parse_form")
		}
		return
	}
	if fh.Size > maxPackagingImageSize {
		apiError(c, http.StatusRequestEntityTooLarge, "api_image_too_large")
		return
	}
	f, err := fh.Open()
	if err != nil {
		apiBadRequest(c, "api_failed_to_parse_form")
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxPackagingImageSize+1))
	if err != nil || int64(len(body)) > maxPackagingImageSize {
		apiError(c, http.StatusRequestEntityTooLarge, "api_image_too_large")
		return
	}
	ext, err := validateImageBytes(body)
	if err != nil {
		apiBadRequest(c, "api_invalid_file_type")
		return
	}

	uploadDir := UploadDirFromContext(c)
	if err := os.MkdirAll(strainsUploadDir(uploadDir), 0o755); err != nil {
		apiInternalError(c, "api_failed_to_create_directory")
		return
	}
	final := filepath.Join(strainsUploadDir(uploadDir), fmt.Sprintf("strain_%d_packaging_%d%s", strainID, time.Now().UnixNano(), ext))
	if err := os.WriteFile(final, body, 0o644); err != nil {
		apiInternalError(c, "api_failed_to_save_file")
		return
	}
	if err := setPackagingImage(DBFromContext(c), uploadDir, strainID, final); err != nil {
		_ = os.Remove(final)
		apiInternalError(c, "api_failed_to_save_file")
		return
	}
	removePendingPackaging(uploadDir, strainID)
	packagingResponse(c, strainID, "api_packaging_saved")
}

// DeletePackagingImageHandler removes the strain's packaging image.
// DELETE /strains/:id/packaging-image
func DeletePackagingImageHandler(c *gin.Context) {
	strainID, ok := packagingStrainID(c)
	if !ok {
		return
	}
	if err := setPackagingImage(DBFromContext(c), UploadDirFromContext(c), strainID, ""); err != nil {
		apiInternalError(c, "api_failed_to_save_file")
		return
	}
	packagingResponse(c, strainID, "api_packaging_removed")
}
