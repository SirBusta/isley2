package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"isley/logger"
	"isley/utils"
)

// Stock tracking on a strain: "wanted" (on the user's wish list) and
// seeds_added_on (when the current seeds were added to stock). Both follow
// the seed count, so every save that sets seed_count goes through
// resolveStrainStock:
//   - seeds in stock: wanted is cleared (you have it now). The date is the
//     one the user entered; when they left it empty and the strain is being
//     (re)stocked from zero, it's today; otherwise it stays as it was.
//   - no seeds: the date is cleared (nothing to date); wanted is the user's.

// strainStockPrev is what's stored before a save; prevCount < 0 means the
// strain is new.
type strainStockPrev struct {
	Count   int
	Wanted  bool
	AddedOn string
}

var newStrainStock = strainStockPrev{Count: -1}

// strainStock is what to store: wanted as 0/1, seeds_added_on as a string or
// nil (NULL).
type strainStock struct {
	Wanted  int
	AddedOn any
}

var errInvalidSeedsAddedOn = errors.New("api_invalid_seeds_added_on")

// loadStrainStock reads a strain's current stock fields.
func loadStrainStock(q interface {
	QueryRow(string, ...any) *sql.Row
}, id int) (strainStockPrev, error) {
	var p strainStockPrev
	var wanted int
	err := q.QueryRow("SELECT seed_count, wanted, coalesce(seeds_added_on, '') FROM strain WHERE id = $1", id).
		Scan(&p.Count, &wanted, &p.AddedOn)
	p.Wanted = wanted == 1
	return p, err
}

// resolveStrainStock applies the rules above. wanted and addedOn may be nil
// ("not sent": keep what's stored).
func resolveStrainStock(seedCount int, wanted *bool, addedOn *string, prev strainStockPrev, today string) (strainStock, error) {
	var out strainStock
	date := ""
	if addedOn != nil {
		date = strings.TrimSpace(*addedOn)
		if date != "" {
			if _, err := time.Parse(utils.LayoutDate, date); err != nil {
				return out, errInvalidSeedsAddedOn
			}
		}
	}

	if seedCount <= 0 {
		w := prev.Wanted
		if wanted != nil {
			w = *wanted
		}
		if w {
			out.Wanted = 1
		}
		return out, nil
	}

	restocking := prev.Count <= 0
	switch {
	case date != "":
		out.AddedOn = date
	case restocking:
		out.AddedOn = today
	case addedOn == nil && prev.AddedOn != "":
		out.AddedOn = prev.AddedOn
	}
	return out, nil
}

// stockToday is today's date for an automatic seeds_added_on, in the
// configured timezone when one is set.
func stockToday(c *gin.Context) string {
	now := time.Now()
	if tz := ConfigStoreFromContext(c).Timezone(); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			now = now.In(loc)
		}
	}
	return now.Format(utils.LayoutDate)
}

// SetStrainWantedHandler adds a strain to, or removes it from, the wish
// list. A strain with seeds in stock can't be wanted.
// POST /strains/:id/wanted  {"wanted": true}
func SetStrainWantedHandler(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apiBadRequest(c, "api_invalid_strain_id")
		return
	}
	var req struct {
		Wanted bool `json:"wanted"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiBadRequest(c, "api_invalid_request_payload")
		return
	}
	db := DBFromContext(c)
	wanted := 0
	if req.Wanted {
		wanted = 1
	}
	res, err := db.Exec("UPDATE strain SET wanted = CASE WHEN seed_count > 0 THEN 0 ELSE $1 END WHERE id = $2", wanted, id)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			apiNotFound(c, "api_strain_not_found")
			return
		}
	}
	var stored int
	if err == nil {
		err = db.QueryRow("SELECT wanted FROM strain WHERE id = $1", id).Scan(&stored)
	}
	if err != nil {
		logger.Log.WithError(err).Error("Failed to set wanted")
		apiInternalError(c, "api_failed_to_update_strain")
		return
	}
	ConfigStoreFromContext(c).SetStrains(GetStrains(db))
	c.JSON(http.StatusOK, gin.H{"wanted": stored == 1})
}
