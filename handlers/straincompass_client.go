package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"isley/logger"
)

// ---------------------------------------------------------------------------
// StrainCompass public API client.
//
// StrainCompass (https://straincompass.com) is a cannabis strain database
// with a documented REST API at straincompass.com/api/. Isley uses it as a
// second strain-lookup source alongside CannaDB, since it carries
// cannabinoid/effects/flavor/terpene data CannaDB doesn't provide. Its Terms
// of Service (§5) explicitly sanctions API use ("automated scraping without
// API access is prohibited"), including a real keyless tier — auth is an
// optional Bearer API key, not a hard requirement.
// ---------------------------------------------------------------------------

const (
	// straincompassDefaultBaseURL is the REST API base. Overridable via the
	// straincompass.base_url setting (trailing slash optional).
	straincompassDefaultBaseURL = "https://straincompass.com/api/"

	// straincompassWebBaseURL is the human-facing site used to build "View
	// on StrainCompass" links. Distinct host path from the API is not
	// needed here since both live on the same domain.
	straincompassWebBaseURL = "https://straincompass.com"

	// straincompassMaxRetries bounds retries on 429/5xx before giving up.
	straincompassMaxRetries = 4
	// straincompassBackoffBase is the base delay for exponential backoff.
	straincompassBackoffBase = 500 * time.Millisecond
	// straincompassBackoffMax caps a single backoff sleep.
	straincompassBackoffMax = 8 * time.Second
	// straincompassResponseCap bounds an inbound response body.
	straincompassResponseCap = 4 * 1024 * 1024

	// straincompassKeylessResultCap is the documented per-request result
	// ceiling for unauthenticated/no-key requests (250/day, 24/request).
	straincompassKeylessResultCap = 24
)

func straincompassUserAgent() string {
	return fmt.Sprintf("Isley/%s (+https://github.com/dwot/isley)", Version)
}

// straincompassBaseURL returns the configured base URL or the default,
// normalized to a single trailing slash.
func straincompassBaseURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		raw = straincompassDefaultBaseURL
	}
	return strings.TrimRight(raw, "/") + "/"
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

// straincompassError is the error envelope for non-2xx responses. The API
// doesn't document a structured error body, so Message is best-effort and
// callers should primarily dispatch on HTTPStatus.
type straincompassError struct {
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *straincompassError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("straincompass: %s (http %d)", e.Message, e.HTTPStatus)
	}
	return fmt.Sprintf("straincompass: http %d", e.HTTPStatus)
}


type straincompassEffect struct {
	Name      string   `json:"name"`
	Intensity *float64 `json:"intensity"`
}

type straincompassFlavor struct {
	Name string `json:"name"`
}

// straincompassTerpene's Level is a qualitative string enum ("low"/"medium"/
// "high" as observed live), not a numeric value.
type straincompassTerpene struct {
	Name  string  `json:"name"`
	Level *string `json:"level"`
}

type straincompassMedicalUse struct {
	Name string `json:"name"`
}

// straincompassAttributeProvenance marks each attribute group as "sourced"
// (real, strain-specific data) or "rule" (generic category-based filler,
// e.g. "typical terpenes for hybrid strains"). Isley only ever imports
// "sourced" groups — "rule" data is never persisted.
type straincompassAttributeProvenance struct {
	Effects  string `json:"effects"`
	Terpenes string `json:"terpenes"`
	Flavors  string `json:"flavors"`
	Medical  string `json:"medical"`
}

const straincompassProvenanceSourced = "sourced"

// straincompassStrain is the full strain record returned by the list/filter
// endpoint. Unused upstream fields (floweringType, growDifficulty,
// popularity scoring, etc.) are intentionally not modeled here.
type straincompassStrain struct {
	ID                  string                           `json:"id"`
	Slug                string                           `json:"slug"`
	Name                string                           `json:"name"`
	Type                string                           `json:"type"`
	IndicaPercent       *int                             `json:"indicaPercent"`
	SativaPercent       *int                             `json:"sativaPercent"`
	ThcMin              *float64                         `json:"thcMin"`
	ThcMax              *float64                         `json:"thcMax"`
	CbdMin              *float64                         `json:"cbdMin"`
	CbdMax              *float64                         `json:"cbdMax"`
	CbnMax              *float64                         `json:"cbnMax"`
	CbgMax              *float64                         `json:"cbgMax"`
	Description         string                           `json:"description"`
	ShortDescription    string                           `json:"shortDescription"`
	Lineage             string                           `json:"lineage"`
	Effects             []straincompassEffect            `json:"effects"`
	Flavors             []straincompassFlavor            `json:"flavors"`
	Terpenes            []straincompassTerpene           `json:"terpenes"`
	MedicalUses         []straincompassMedicalUse        `json:"medicalUses"`
	FloweringTimeMin    *int                             `json:"floweringTimeMin"`
	FloweringTimeMax    *int                             `json:"floweringTimeMax"`
	HeightIndoor        *string                          `json:"heightIndoor"` // free text, e.g. "90-150cm"
	HeightOutdoor       *string                          `json:"heightOutdoor"`
	YieldIndoor         *string                          `json:"yieldIndoor"` // free text, e.g. "450-550 g/m²"
	YieldOutdoor        *string                          `json:"yieldOutdoor"`
	Breeder             string                           `json:"breeder"`
	Sources             []string                         `json:"sources"`
	Verified            bool                             `json:"verified"`
	QualityScore        *float64                         `json:"qualityScore"`
	AttributeProvenance straincompassAttributeProvenance `json:"attributeProvenance"`
	UpdatedAt           string                           `json:"updatedAt"`
}

type straincompassListResponse struct {
	Strains    []straincompassStrain `json:"strains"`
	Total      int                   `json:"total"`
	Page       int                   `json:"page"`
	TotalPages int                   `json:"totalPages"`
}

// ---------------------------------------------------------------------------
// Web-link derivation
// ---------------------------------------------------------------------------

// StraincompassWebURL is the exported wrapper used by the view layer to
// render a "View on StrainCompass" link from a stored strain slug.
func StraincompassWebURL(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	return straincompassWebBaseURL + "/strains/" + url.PathEscape(slug)
}

// ---------------------------------------------------------------------------
// HTTP plumbing
// ---------------------------------------------------------------------------

// straincompassGet issues GET <base><path>?<params>, decoding a 200 body
// into out. Sends an Authorization: Bearer header only when apiKey is
// non-empty — keyless is a fully valid, documented operating mode, not a
// fallback. Honors Retry-After and backs off with jitter on 429/5xx.
func straincompassGet(baseURL, apiKey, path string, params url.Values, out interface{}) error {
	fieldLogger := logger.Log.WithField("func", "straincompassGet").WithField("path", path)

	endpoint := straincompassBaseURL(baseURL) + path
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= straincompassMaxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", straincompassUserAgent())
		req.Header.Set("Accept", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if attempt < straincompassMaxRetries {
				time.Sleep(straincompassBackoff(attempt))
				continue
			}
			return err
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, straincompassResponseCap))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < straincompassMaxRetries {
				time.Sleep(straincompassBackoff(attempt))
				continue
			}
			return readErr
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("straincompass: decode %s: %w", path, err)
			}
			return nil

		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			// Bad/rejected API key — not retryable, and distinct from a
			// generic failure so the caller can surface a clear message.
			return decodeStraincompassError(resp.StatusCode, body)

		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			apiErr := decodeStraincompassError(resp.StatusCode, body)
			lastErr = apiErr
			if attempt < straincompassMaxRetries {
				wait := straincompassRetryWait(attempt, resp.Header.Get("Retry-After"))
				fieldLogger.WithField("status", resp.StatusCode).WithField("wait", wait).
					Debug("StrainCompass backpressure, backing off")
				time.Sleep(wait)
				continue
			}
			return apiErr

		default:
			// 4xx other than 401/403/429 — not retryable.
			return decodeStraincompassError(resp.StatusCode, body)
		}
	}
	return lastErr
}

func decodeStraincompassError(status int, body []byte) *straincompassError {
	apiErr := &straincompassError{HTTPStatus: status}
	_ = json.Unmarshal(body, apiErr) // best-effort; body may not be JSON
	return apiErr
}

// straincompassBackoff returns the exponential backoff delay for an attempt,
// with full jitter, capped at straincompassBackoffMax.
func straincompassBackoff(attempt int) time.Duration {
	d := straincompassBackoffBase << attempt
	if d > straincompassBackoffMax {
		d = straincompassBackoffMax
	}
	half := d / 2
	return half + time.Duration(rand.Int63n(int64(half)+1))
}

// straincompassRetryWait honors a server's Retry-After header exactly
// (including "0"), falling back to jittered exponential backoff.
func straincompassRetryWait(attempt int, header string) time.Duration {
	if header = strings.TrimSpace(header); header != "" {
		if secs, err := strconv.Atoi(header); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return straincompassBackoff(attempt)
}

// ---------------------------------------------------------------------------
// High-level calls
// ---------------------------------------------------------------------------

// straincompassListStrains searches the list endpoint, which returns full
// listings including each one's breeder (the lighter strains/search endpoint
// doesn't, and StrainCompass has one listing per seed company, so the
// breeder is what tells them apart). Results aren't relevance-ordered. The
// API requires a 2-character minimum query; callers must enforce that.
// Returns the listings and the total number of matches.
func straincompassListStrains(baseURL, apiKey, query string, limit int) ([]straincompassStrain, int, error) {
	if limit <= 0 || limit > straincompassKeylessResultCap {
		limit = straincompassKeylessResultCap
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(limit))

	var out straincompassListResponse
	if err := straincompassGet(baseURL, apiKey, "strains", params, &out); err != nil {
		return nil, 0, err
	}
	return out.Strains, out.Total, nil
}

// errStraincompassNotFound is returned by straincompassGetStrainBySlug when
// no result exactly matches the requested slug, even on an HTTP 200 (the
// list endpoint can return near-matches instead of an exact hit).
var errStraincompassNotFound = fmt.Errorf("straincompass: no exact slug match")

// straincompassGetStrainBySlug fetches a full strain record. StrainCompass
// has no dedicated single-strain endpoint (GET /api/strains/<slug> 404s), so
// this goes through the list/filter endpoint and verifies the returned row's
// slug matches exactly before trusting it.
//
// The list endpoint's q parameter matches against the strain's display name,
// not its slug (a hyphenated slug like "permanent-marker" does not match the
// name "Permanent Marker" — confirmed against the live API), so the query
// text must be the name, not the slug. name may be empty (e.g. from a client
// that only has the slug), in which case the slug is used as a best-effort
// query, which works for single-word strain names but not multi-word ones.
//
// Popular names have dozens of listings (one per seed company), so breeder —
// the chosen listing's breeder, when known — narrows the query with the
// list endpoint's breeder filter; if that finds nothing the lookup is
// retried without it.
func straincompassGetStrainBySlug(baseURL, apiKey, slug, name, breeder string) (*straincompassStrain, error) {
	queryText := strings.TrimSpace(name)
	if queryText == "" {
		queryText = slug
	}
	breeder = strings.TrimSpace(breeder)

	find := func(withBreeder bool) (*straincompassStrain, error) {
		params := url.Values{}
		params.Set("q", queryText)
		params.Set("limit", strconv.Itoa(straincompassKeylessResultCap))
		if withBreeder {
			params.Set("breeder", breeder)
		}
		var out straincompassListResponse
		if err := straincompassGet(baseURL, apiKey, "strains", params, &out); err != nil {
			return nil, err
		}
		for i := range out.Strains {
			if out.Strains[i].Slug == slug {
				return &out.Strains[i], nil
			}
		}
		return nil, errStraincompassNotFound
	}

	if breeder != "" {
		rec, err := find(true)
		if !errors.Is(err, errStraincompassNotFound) {
			return rec, err
		}
	}
	return find(false)
}
