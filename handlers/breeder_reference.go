package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"isley/config"
	"isley/logger"

	"github.com/gin-gonic/gin"
)

const contextKeyAssets = "assets"

// breederReferencePath is the bundled snapshot of known breeder names that
// seeds the breeder type-ahead until the first successful refresh.
const breederReferencePath = "web/static/data/breeders.json"

// BreederReferenceSourceURL is StrainCompass's public breeder directory: one
// page listing every breeder as a /breeders/<name> link.
const BreederReferenceSourceURL = "https://straincompass.com/breeders"

const (
	breederRefInterval      = 30 * 24 * time.Hour
	breederRefRetryInterval = 7 * 24 * time.Hour
	breederRefCheckEvery    = 24 * time.Hour
	breederRefStartDelay    = 2 * time.Minute
	breederRefMinNames      = 100
	breederRefMaxBody       = 8 << 20

	settingBreederRefAuto        = "breeder_reference.auto_refresh"
	settingBreederRefETag        = "breeder_reference.etag"
	settingBreederRefLastAttempt = "breeder_reference.last_attempt"
	settingBreederRefLastUpdated = "breeder_reference.last_updated"
	settingBreederRefLastError   = "breeder_reference.last_error"
)

var breederRefClient = &http.Client{Timeout: 30 * time.Second}

// SetAssetsOnContext binds the engine's embedded asset FS to a request.
func SetAssetsOnContext(c *gin.Context, assets fs.FS) {
	c.Set(contextKeyAssets, assets)
}

// AssetsFromContext returns the embedded asset FS, or nil when none was bound.
func AssetsFromContext(c *gin.Context) fs.FS {
	v, ok := c.Get(contextKeyAssets)
	if !ok {
		return nil
	}
	assets, _ := v.(fs.FS)
	return assets
}

func sortNamesFold(names []string) {
	sort.SliceStable(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
}

// loadBundledBreederReference reads the breeder names from the bundled
// snapshot, trimmed and without blanks, sorted case-insensitively.
func loadBundledBreederReference(assets fs.FS) ([]string, error) {
	if assets == nil {
		return nil, errors.New("no embedded assets")
	}
	data, err := fs.ReadFile(assets, breederReferencePath)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Breeders []struct {
			Name string `json:"name"`
		} `json:"breeders"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doc.Breeders))
	for _, b := range doc.Breeders {
		if n := strings.TrimSpace(b.Name); n != "" {
			names = append(names, n)
		}
	}
	sortNamesFold(names)
	return names, nil
}

func loadStoredBreederReference(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM breeder_reference")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortNamesFold(names)
	return names, nil
}

// loadBreederReference returns the refreshed list when one has been stored,
// otherwise the bundled snapshot. source is "refreshed" or "bundled".
func loadBreederReference(db *sql.DB, assets fs.FS) (names []string, source string, err error) {
	stored, err := loadStoredBreederReference(db)
	if err != nil {
		logger.Log.WithField("func", "loadBreederReference").WithError(err).Warn("Failed to read stored breeder reference; using bundled list")
	}
	if len(stored) > 0 {
		return stored, "refreshed", nil
	}
	names, err = loadBundledBreederReference(assets)
	return names, "bundled", err
}

// BreederReferenceHandler serves the list of known breeder names used as
// type-ahead suggestions. These are suggestions only; they are never
// inserted into the breeder table unless the user picks one.
func BreederReferenceHandler(c *gin.Context) {
	names, _, err := loadBreederReference(DBFromContext(c), AssetsFromContext(c))
	if err != nil {
		logger.Log.WithField("func", "BreederReferenceHandler").WithError(err).Error("Failed to load breeder reference list")
		names = []string{}
	}
	if names == nil {
		names = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"names": names})
}

// ---------------------------------------------------------------------------
// Monthly refresh from StrainCompass's public breeder directory
// ---------------------------------------------------------------------------

var breederLinkRe = regexp.MustCompile(`href="/breeders/([^"?#/]+)"`)

// parseBreederDirectory extracts breeder names from the directory page's
// /breeders/<url-encoded name> links. Reading link targets rather than the
// visible markup keeps this working through layout changes.
func parseBreederDirectory(page string) []string {
	seen := map[string]bool{}
	var names []string
	for _, m := range breederLinkRe.FindAllStringSubmatch(page, -1) {
		raw, err := url.PathUnescape(html.UnescapeString(m[1]))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(repairMojibake(raw))
		switch strings.ToLower(name) {
		case "", "null", "unknown", "unknown breeder":
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// repairMojibake undoes UTF-8 text that was decoded as Latin-1 somewhere
// upstream (e.g. "Doctorâ\u0080\u0099s" -> "Doctor’s"). Strings that don't
// round-trip to valid UTF-8, such as a genuine "Café", are left alone.
func repairMojibake(s string) string {
	suspicious := false
	for _, r := range s {
		if r > 0xFF {
			return s
		}
		if r >= 0x80 {
			suspicious = true
		}
	}
	if !suspicious {
		return s
	}
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	if !utf8.Valid(b) {
		return s
	}
	return string(b)
}

// BreederReferenceResult describes one refresh attempt.
type BreederReferenceResult struct {
	Updated bool // the stored list was replaced
	Count   int  // names in the list now in effect
}

// RefreshBreederReference fetches the breeder directory and, if it looks
// sane, replaces the stored reference list. It sends one conditional GET
// (If-None-Match) and keeps the current list on any failure or on a result
// with fewer than half the previous names.
func RefreshBreederReference(ctx context.Context, db *sql.DB, assets fs.FS, sourceURL string) (BreederReferenceResult, error) {
	res, err := refreshBreederReference(ctx, db, assets, sourceURL)
	now := time.Now().UTC().Format(time.RFC3339)
	_ = UpdateSetting(db, nil, settingBreederRefLastAttempt, now)
	if err != nil {
		_ = UpdateSetting(db, nil, settingBreederRefLastError, err.Error())
		return res, err
	}
	_ = UpdateSetting(db, nil, settingBreederRefLastError, "")
	if res.Updated {
		_ = UpdateSetting(db, nil, settingBreederRefLastUpdated, now)
	}
	return res, nil
}

func refreshBreederReference(ctx context.Context, db *sql.DB, assets fs.FS, sourceURL string) (BreederReferenceResult, error) {
	current, source, _ := loadBreederReference(db, assets)
	res := BreederReferenceResult{Count: len(current)}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("User-Agent", straincompassUserAgent())
	req.Header.Set("Accept", "text/html")
	if source == "refreshed" {
		if etag, _ := GetSetting(db, settingBreederRefETag); etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
	}

	resp, err := breederRefClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("fetch breeder directory: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return res, nil
	}
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("fetch breeder directory: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, breederRefMaxBody))
	if err != nil {
		return res, fmt.Errorf("read breeder directory: %w", err)
	}

	names := parseBreederDirectory(string(body))
	minimum := breederRefMinNames
	if half := len(current) / 2; half > minimum {
		minimum = half
	}
	if len(names) < minimum {
		return res, fmt.Errorf("breeder directory returned %d names (previous list has %d); keeping the previous list", len(names), len(current))
	}

	if err := replaceStoredBreederReference(db, names); err != nil {
		return res, fmt.Errorf("store breeder reference: %w", err)
	}
	_ = UpdateSetting(db, nil, settingBreederRefETag, resp.Header.Get("ETag"))
	return BreederReferenceResult{Updated: true, Count: len(names)}, nil
}

func replaceStoredBreederReference(db *sql.DB, names []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("DELETE FROM breeder_reference"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO breeder_reference (name) VALUES ($1)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, n := range names {
		if _, err := stmt.Exec(n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func breederRefAutoEnabled(db *sql.DB) bool {
	v, _ := GetSetting(db, settingBreederRefAuto)
	return v != "0" // on unless explicitly turned off
}

// breederRefDue reports whether an automatic refresh should run now: 30 days
// after the last attempt, or 7 days after a failed one.
func breederRefDue(db *sql.DB, now time.Time) bool {
	last, _ := GetSetting(db, settingBreederRefLastAttempt)
	failed, _ := GetSetting(db, settingBreederRefLastError)
	return breederRefDueAt(last, failed, now)
}

func breederRefDueAt(lastAttempt, lastError string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, lastAttempt)
	if err != nil {
		return true
	}
	if lastError != "" {
		return now.Sub(t) >= breederRefRetryInterval
	}
	return now.Sub(t) >= breederRefInterval
}

// RunBreederReferenceRefresher checks once a day whether the monthly refresh
// is due. It blocks until ctx is cancelled; run it in its own goroutine.
func RunBreederReferenceRefresher(ctx context.Context, db *sql.DB, assets fs.FS) {
	fieldLogger := logger.Log.WithField("func", "RunBreederReferenceRefresher")
	timer := time.NewTimer(breederRefStartDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if !config.RestoreInProgress.Load() && breederRefAutoEnabled(db) && breederRefDue(db, time.Now()) {
			res, err := RefreshBreederReference(ctx, db, assets, BreederReferenceSourceURL)
			if err != nil {
				fieldLogger.WithError(err).Warn("Breeder reference refresh failed; keeping the previous list")
			} else {
				fieldLogger.WithField("updated", res.Updated).WithField("count", res.Count).Info("Breeder reference refresh completed")
			}
		}
		timer.Reset(breederRefCheckEvery)
	}
}

// BreederReferenceStatus is what the Settings card shows.
type BreederReferenceStatus struct {
	AutoRefresh bool   `json:"auto_refresh"`
	Source      string `json:"source"` // "bundled" or "refreshed"
	Count       int    `json:"count"`
	LastAttempt string `json:"last_attempt"`
	LastUpdated string `json:"last_updated"`
	LastError   string `json:"last_error"`
}

func breederReferenceStatus(db *sql.DB, assets fs.FS) BreederReferenceStatus {
	names, source, _ := loadBreederReference(db, assets)
	lastAttempt, _ := GetSetting(db, settingBreederRefLastAttempt)
	lastUpdated, _ := GetSetting(db, settingBreederRefLastUpdated)
	lastError, _ := GetSetting(db, settingBreederRefLastError)
	return BreederReferenceStatus{
		AutoRefresh: breederRefAutoEnabled(db),
		Source:      source,
		Count:       len(names),
		LastAttempt: lastAttempt,
		LastUpdated: lastUpdated,
		LastError:   lastError,
	}
}

// BreederReferenceStatusHandler: GET /breeders/reference/status
func BreederReferenceStatusHandler(c *gin.Context) {
	c.JSON(http.StatusOK, breederReferenceStatus(DBFromContext(c), AssetsFromContext(c)))
}

// BreederReferenceSettingsHandler: POST /breeders/reference/settings {"auto_refresh": bool}
func BreederReferenceSettingsHandler(c *gin.Context) {
	var req struct {
		AutoRefresh *bool `json:"auto_refresh"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.AutoRefresh == nil {
		apiBadRequest(c, "api_invalid_request_payload")
		return
	}
	db := DBFromContext(c)
	val := "0"
	if *req.AutoRefresh {
		val = "1"
	}
	if err := UpdateSetting(db, nil, settingBreederRefAuto, val); err != nil {
		apiInternalError(c, "api_failed_to_save_settings")
		return
	}
	c.JSON(http.StatusOK, breederReferenceStatus(db, AssetsFromContext(c)))
}

// BreederReferenceRefreshHandler: POST /breeders/reference/refresh — refresh now.
func BreederReferenceRefreshHandler(c *gin.Context) {
	db := DBFromContext(c)
	assets := AssetsFromContext(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	if _, err := RefreshBreederReference(ctx, db, assets, BreederReferenceSourceURL); err != nil {
		logger.Log.WithField("func", "BreederReferenceRefreshHandler").WithError(err).Warn("Breeder reference refresh failed")
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  T(c, "api_breeder_reference_refresh_failed"),
			"status": breederReferenceStatus(db, assets),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": T(c, "api_breeder_reference_refreshed"),
		"status":  breederReferenceStatus(db, assets),
	})
}
