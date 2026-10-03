package handlers

// Unit tests for the StrainCompass client (handlers/straincompass_client.go).
// These are in package handlers (not handlers_test) so they can exercise the
// unexported client surface directly without standing up the HTTP server.

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestStraincompassWebURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		slug string
		want string
	}{
		{name: "normal", slug: "permanent-marker", want: "https://straincompass.com/strains/permanent-marker"},
		{name: "empty", slug: "", want: ""},
		{name: "whitespace only", slug: "   ", want: ""},
		{name: "needs escaping", slug: "og kush", want: "https://straincompass.com/strains/og%20kush"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StraincompassWebURL(tc.slug); got != tc.want {
				t.Fatalf("StraincompassWebURL(%q) = %q, want %q", tc.slug, got, tc.want)
			}
		})
	}
}

func TestNormalizeIndicaSativa(t *testing.T) {
	t.Parallel()

	t.Run("both present, already sums to 100", func(t *testing.T) {
		indica, sativa := normalizeIndicaSativa(intPtr(70), intPtr(30))
		if indica != 70 || sativa != 30 {
			t.Fatalf("indica/sativa = %d/%d, want 70/30", indica, sativa)
		}
	})

	t.Run("both present, needs proportional rescale", func(t *testing.T) {
		// 40 indica + 40 sativa (doesn't sum to 100) -> equal split, 50/50.
		indica, sativa := normalizeIndicaSativa(intPtr(40), intPtr(40))
		if indica != 50 || sativa != 50 {
			t.Fatalf("indica/sativa = %d/%d, want 50/50", indica, sativa)
		}
		if indica+sativa != 100 {
			t.Fatal("indica+sativa must equal 100")
		}
	})

	t.Run("both present but both zero avoids divide-by-zero", func(t *testing.T) {
		indica, sativa := normalizeIndicaSativa(intPtr(0), intPtr(0))
		if indica != 50 || sativa != 50 {
			t.Fatalf("indica/sativa = %d/%d, want 50/50 default", indica, sativa)
		}
	})

	t.Run("only sativa present", func(t *testing.T) {
		indica, sativa := normalizeIndicaSativa(nil, intPtr(25))
		if indica != 75 || sativa != 25 {
			t.Fatalf("indica/sativa = %d/%d, want 75/25", indica, sativa)
		}
	})

	t.Run("only indica present", func(t *testing.T) {
		indica, sativa := normalizeIndicaSativa(intPtr(80), nil)
		if indica != 80 || sativa != 20 {
			t.Fatalf("indica/sativa = %d/%d, want 80/20", indica, sativa)
		}
	})

	t.Run("neither present defaults 50/50", func(t *testing.T) {
		indica, sativa := normalizeIndicaSativa(nil, nil)
		if indica != 50 || sativa != 50 {
			t.Fatalf("indica/sativa = %d/%d, want 50/50", indica, sativa)
		}
	})

	t.Run("out-of-range values clamped", func(t *testing.T) {
		indica, sativa := normalizeIndicaSativa(intPtr(150), nil)
		if indica != 100 || sativa != 0 {
			t.Fatalf("clamp failed: %d/%d", indica, sativa)
		}
	})
}

func TestMapStraincompassStrain(t *testing.T) {
	t.Parallel()

	t.Run("cycleTime prefers max, falls back to min, converts weeks to days", func(t *testing.T) {
		// floweringTimeMin/Max are in weeks on the live API; cycle_time is
		// days, so mapStraincompassStrain must multiply by 7.
		rec := &straincompassStrain{Name: "X", FloweringTimeMax: intPtr(9), FloweringTimeMin: intPtr(8)}
		s := mapStraincompassStrain(rec)
		if s.CycleTime != 63 {
			t.Fatalf("cycleTime = %d, want 63 (9 weeks max -> days)", s.CycleTime)
		}

		rec2 := &straincompassStrain{Name: "X", FloweringTimeMin: intPtr(10)}
		s2 := mapStraincompassStrain(rec2)
		if s2.CycleTime != 70 {
			t.Fatalf("cycleTime = %d, want 70 (10 weeks min fallback -> days)", s2.CycleTime)
		}
	})

	t.Run("verified and sources carried through", func(t *testing.T) {
		rec := &straincompassStrain{
			Name:     "X",
			Verified: true,
			Sources:  []string{"seedfinder", "leafly"},
			Slug:     "x",
		}
		s := mapStraincompassStrain(rec)
		if s.StraincompassVerified == nil || !*s.StraincompassVerified {
			t.Fatalf("expected StraincompassVerified=true, got %+v", s.StraincompassVerified)
		}
		if s.StraincompassSources != "seedfinder,leafly" {
			t.Fatalf("sources = %q, want %q", s.StraincompassSources, "seedfinder,leafly")
		}
		if s.StraincompassSlug != "x" {
			t.Fatalf("slug = %q, want %q", s.StraincompassSlug, "x")
		}
	})

	t.Run("autoflower pre-set from floweringType, else from the name", func(t *testing.T) {
		cases := []struct {
			name, floweringType string
			want                bool
		}{
			{"Haze Automatic", "AUTOFLOWER", true},
			{"Plain Name", "AUTOFLOWER", true},
			{"White Widow Automatic", "UNKNOWN", true}, // the live record that prompted this
			{"Gelato Auto", "", true},
			{"Dfa Autoflowering", "UNKNOWN", true},
			{"auto blow dream (aka: auto blue dream)", "UNKNOWN", true},
			{"Blue Dream", "UNKNOWN", false},
			{"Autobahn Kush", "UNKNOWN", false}, // "auto" only as a whole word
			{"Northern Lights Auto", "PHOTOPERIOD", false},
		}
		for _, tc := range cases {
			s := mapStraincompassStrain(&straincompassStrain{Name: tc.name, FloweringType: tc.floweringType})
			if s.Autoflower != tc.want {
				t.Errorf("%q (%q): Autoflower = %v, want %v", tc.name, tc.floweringType, s.Autoflower, tc.want)
			}
		}
	})

	t.Run("description imported directly", func(t *testing.T) {
		rec := &straincompassStrain{Name: "X", Description: "Marketing copy from a seed bank.", ShortDescription: "Short."}
		s := mapStraincompassStrain(rec)
		if s.Description != "Marketing copy from a seed bank." || s.ShortDescription != "Short." {
			t.Fatalf("unexpected description mapping: %+v", s)
		}
	})
}

func TestStraincompassGetStrainBySlug_UsesListingBreeder(t *testing.T) {
	t.Parallel()

	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		queries = append(queries, "breeder="+q.Get("breeder")+"&limit="+q.Get("limit"))
		w.WriteHeader(http.StatusOK)
		switch q.Get("breeder") {
		case "Humboldt Seed Co":
			_, _ = w.Write([]byte(`{"strains":[{"slug":"blue-dream-humboldt","name":"Blue Dream","breeder":"Humboldt Seed Co"}]}`))
		case "Wrong Breeder":
			_, _ = w.Write([]byte(`{"strains":[]}`))
		default:
			_, _ = w.Write([]byte(`{"strains":[{"slug":"blue-dream-humboldt","name":"Blue Dream","breeder":"Humboldt Seed Co"}]}`))
		}
	}))
	defer srv.Close()

	rec, err := straincompassGetStrainBySlug(srv.URL+"/api/", "", "blue-dream-humboldt", "Blue Dream", "Humboldt Seed Co")
	if err != nil || rec.Slug != "blue-dream-humboldt" {
		t.Fatalf("got %+v, %v", rec, err)
	}
	if len(queries) != 1 || queries[0] != "breeder=Humboldt Seed Co&limit=24" {
		t.Fatalf("expected one breeder-filtered lookup of up to 24, got %v", queries)
	}

	// A breeder filter that finds nothing falls back to the name alone.
	queries = nil
	rec, err = straincompassGetStrainBySlug(srv.URL+"/api/", "", "blue-dream-humboldt", "Blue Dream", "Wrong Breeder")
	if err != nil || rec.Slug != "blue-dream-humboldt" {
		t.Fatalf("fallback: got %+v, %v", rec, err)
	}
	if len(queries) != 2 || queries[1] != "breeder=&limit=24" {
		t.Fatalf("expected filtered then unfiltered lookups, got %v", queries)
	}
}

func TestRankStraincompassListings(t *testing.T) {
	t.Parallel()

	recs := []straincompassStrain{
		{Slug: "a", Name: "Blue Dream Bx", Breeder: "Seedsman"},
		{Slug: "b", Name: "Super Blue Dream", Breeder: "Alpha"},
		{Slug: "c", Name: "Blue Dream", Breeder: " Humboldt Seed Co "},
		{Slug: "d", Name: "Dream Queen", Breeder: "Zeta"},
		{Slug: "e", Name: "blue dream", Breeder: "Barneys Farm"},
	}
	got := rankStraincompassListings(recs, "Blue Dream")
	order := ""
	for _, r := range got {
		order += r.Slug
	}
	// Exact matches (by breeder), then prefix, then contains, then the rest.
	if order != "ecabd" {
		t.Fatalf("order = %q, want %q", order, "ecabd")
	}
	if got[1].Breeder != "Humboldt Seed Co" {
		t.Fatalf("breeder not trimmed: %q", got[1].Breeder)
	}
}

func TestStraincompassGet_RetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	// Each 429 carries Retry-After: 0, so the client retries immediately with
	// no real sleep — keeping the test fast and parallel-safe.
	var calls int32
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"strains":[{"slug":"og-kush","name":"OG Kush","type":"HYBRID","breeder":"Seedsman"}],"total":42}`))
	}))
	defer srv.Close()

	rows, total, err := straincompassListStrains(srv.URL+"/api/", "", "og", 5)
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
	if gotPath != "/api/strains" {
		t.Fatalf("expected the list endpoint (it carries breeders), got %q", gotPath)
	}
	if len(rows) != 1 || rows[0].Name != "OG Kush" || rows[0].Breeder != "Seedsman" || total != 42 {
		t.Fatalf("unexpected rows/total: %+v %d", rows, total)
	}
}

func TestStraincompassGet_NonRetryableError(t *testing.T) {
	t.Parallel()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer srv.Close()

	_, _, err := straincompassListStrains(srv.URL+"/api/", "", "og", 5)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*straincompassError)
	if !ok {
		t.Fatalf("expected *straincompassError, got %T: %v", err, err)
	}
	if apiErr.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("unexpected error envelope: %+v", apiErr)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("400 must not be retried, got %d calls", calls)
	}
}

func TestStraincompassGet_InvalidKeyNotRetried(t *testing.T) {
	t.Parallel()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	defer srv.Close()

	_, _, err := straincompassListStrains(srv.URL+"/api/", "bad-key", "og", 5)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*straincompassError)
	if !ok || apiErr.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("expected 401 *straincompassError, got %T: %v", err, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("401 must not be retried, got %d calls", calls)
	}
}

func TestStraincompassGet_SendsBearerTokenOnlyWhenConfigured(t *testing.T) {
	t.Parallel()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"strains":[]}`))
	}))
	defer srv.Close()

	if _, _, err := straincompassListStrains(srv.URL+"/api/", "my-key", "og", 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer my-key" {
		t.Fatalf("Authorization header = %q, want %q", gotAuth, "Bearer my-key")
	}

	if _, _, err := straincompassListStrains(srv.URL+"/api/", "", "og", 5); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization header = %q, want empty for keyless request", gotAuth)
	}
}

func TestStraincompassGetStrainBySlug_ExactMatchRequired(t *testing.T) {
	t.Parallel()

	t.Run("returns not-found when only a near-match exists", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"strains":[{"slug":"permanent-marker-auto","name":"Permanent Marker Auto"}],"total":1}`))
		}))
		defer srv.Close()

		_, err := straincompassGetStrainBySlug(srv.URL+"/api/", "", "permanent-marker", "Permanent Marker", "")
		if err != errStraincompassNotFound {
			t.Fatalf("expected errStraincompassNotFound, got %v", err)
		}
	})

	t.Run("returns the exact match when present among several rows", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"strains":[
				{"slug":"permanent-marker-auto","name":"Permanent Marker Auto"},
				{"slug":"permanent-marker","name":"Permanent Marker"}
			],"total":2}`))
		}))
		defer srv.Close()

		rec, err := straincompassGetStrainBySlug(srv.URL+"/api/", "", "permanent-marker", "Permanent Marker", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.Name != "Permanent Marker" {
			t.Fatalf("got %+v, want the exact-slug row", rec)
		}
	})
}

func TestStraincompassGetStrainBySlug_QueryUsesNameNotSlug(t *testing.T) {
	t.Parallel()

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"strains":[{"slug":"permanent-marker","name":"Permanent Marker"}],"total":1}`))
	}))
	defer srv.Close()

	// The list endpoint matches on name, not slug — a hyphenated slug like
	// "permanent-marker" does not match the name "Permanent Marker" on the
	// live API, so the query text sent must be the name when one is given.
	if _, err := straincompassGetStrainBySlug(srv.URL+"/api/", "", "permanent-marker", "Permanent Marker", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "Permanent Marker" {
		t.Fatalf("query = %q, want the name %q", gotQuery, "Permanent Marker")
	}

	// With no name given, falls back to the slug as a best-effort query.
	if _, err := straincompassGetStrainBySlug(srv.URL+"/api/", "", "permanent-marker", "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "permanent-marker" {
		t.Fatalf("query = %q, want the slug fallback %q", gotQuery, "permanent-marker")
	}
}
