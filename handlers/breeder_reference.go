package handlers

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"isley/logger"

	"github.com/gin-gonic/gin"
)

const contextKeyAssets = "assets"

// breederReferencePath is the bundled snapshot of known breeder names that
// seeds the breeder type-ahead. It lives under web/static so it is embedded
// with the rest of the static assets.
const breederReferencePath = "web/static/data/breeders.json"

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

// loadBundledBreederReference reads the breeder names from the bundled
// snapshot, trimmed and without blanks, sorted case-insensitively.
func loadBundledBreederReference(assets fs.FS) ([]string, error) {
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
	sort.SliceStable(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names, nil
}

// BreederReferenceHandler serves the list of known breeder names used as
// type-ahead suggestions. These are suggestions only; they are never
// inserted into the breeder table unless the user picks one.
func BreederReferenceHandler(c *gin.Context) {
	assets := AssetsFromContext(c)
	if assets == nil {
		c.JSON(http.StatusOK, gin.H{"names": []string{}})
		return
	}
	names, err := loadBundledBreederReference(assets)
	if err != nil {
		logger.Log.WithField("func", "BreederReferenceHandler").WithError(err).Error("Failed to load breeder reference list")
		c.JSON(http.StatusOK, gin.H{"names": []string{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"names": names})
}
