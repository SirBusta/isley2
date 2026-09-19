package watcher

import (
	"fmt"
	"strings"
	"time"

	"isley/model"
)

// sqliteRollupIndex is the index the incremental SQLite rollup is pinned to.
// See sqliteRollupQuery for why it is pinned rather than left to the planner.
const sqliteRollupIndex = "idx_sensor_data_create_dt"

// RefreshHourlyRollups aggregates raw sensor_data into the
// sensor_data_hourly rollup table. It processes only the last 25 hours
// of data on each run, using UPSERT to keep existing buckets current
// and add any new ones.
//
// On first run (empty rollup table) it backfills all available history.
func (w *Watcher) RefreshHourlyRollups() error {
	// Check if the rollup table is empty (first run → full backfill).
	var rowCount int
	if err := w.DB.QueryRow("SELECT COUNT(*) FROM sensor_data_hourly").Scan(&rowCount); err != nil {
		return fmt.Errorf("rollup: failed to check rollup table: %w", err)
	}

	if rowCount == 0 {
		w.Logger.Info("Hourly rollup table is empty — running full backfill")
		return w.runRollup(true)
	}

	return w.runRollup(false)
}

// runRollup executes the actual aggregation query. When fullBackfill
// is false it only processes the last 25 hours (overlap by 1 hour to
// catch late-arriving data).
//
// The whole aggregation is one statement, so on SQLite it holds the
// database-wide write lock for as long as it runs; any other write
// arriving in that window fails with "database is locked" (or waits out
// busy_timeout). The logged duration is therefore the length of the
// write-lock hold, which is the number to look at when diagnosing lock
// errors.
func (w *Watcher) runRollup(fullBackfill bool) error {
	start := time.Now()

	var err error
	if model.IsPostgres() {
		_, err = w.DB.Exec(buildPostgresRollupQuery(fullBackfill))
	} else {
		_, err = w.DB.Exec(buildSQLiteRollupQuery(fullBackfill))
		if err != nil && !fullBackfill && strings.Contains(err.Error(), "no such index") {
			// Unlike a planner hint, INDEXED BY is a hard requirement: if the
			// index is missing (e.g. dropped by hand or mid-restore) the
			// statement errors instead of falling back to a scan. Retry
			// without the pin so rollups keep working, just slower.
			w.Logger.WithError(err).Warn("Rollup index " + sqliteRollupIndex + " is missing; retrying without it")
			_, err = w.DB.Exec(sqliteRollupQuery(fullBackfill, false))
		}
	}
	if err != nil {
		return fmt.Errorf("rollup: aggregation query failed: %w", err)
	}

	w.Logger.WithField("duration_ms", time.Since(start).Milliseconds()).Info("Hourly rollup refresh completed")
	return nil
}

// buildSQLiteRollupQuery returns the SQLite rollup statement. The
// incremental (last 25 hours) form is pinned to sqliteRollupIndex; the
// full-backfill form reads everything and is left to the planner.
func buildSQLiteRollupQuery(fullBackfill bool) string {
	return sqliteRollupQuery(fullBackfill, !fullBackfill)
}

// sqliteRollupQuery builds the SQLite rollup statement, optionally pinning
// the read of sensor_data to sqliteRollupIndex.
//
// Why pin: with no sqlite_stat1 statistics the planner cannot tell that the
// 25-hour filter is selective, and because of the GROUP BY on sensor_id it
// chooses to walk idx_sensor_data_sensor_id — a full scan of every row ever
// recorded — instead of range-seeking create_dt. On a multi-million-row
// table that scan held the write lock for ~13s on every 10-minute run.
// Measured on 2.59M rows: full scan 3.45s vs 0.057s pinned. Statistics are
// only ever gathered by the retention prune (which is off by default), so
// the planner cannot be relied on to find the right plan by itself.
func sqliteRollupQuery(fullBackfill, pinIndex bool) string {
	fromClause := "sensor_data sd"
	if pinIndex {
		fromClause += " INDEXED BY " + sqliteRollupIndex
	}

	whereClause := ""
	if !fullBackfill {
		whereClause = "WHERE sd.create_dt > datetime('now', '-25 hours')"
	}

	return fmt.Sprintf(`
		INSERT OR REPLACE INTO sensor_data_hourly (sensor_id, bucket, min_val, max_val, avg_val, sample_count)
		SELECT
			sd.sensor_id,
			strftime('%%Y-%%m-%%d %%H:00:00', sd.create_dt) AS bucket,
			MIN(sd.value),
			MAX(sd.value),
			AVG(sd.value),
			COUNT(*)
		FROM %s
		%s
		GROUP BY sd.sensor_id, strftime('%%Y-%%m-%%d %%H:00:00', sd.create_dt)
	`, fromClause, whereClause)
}

func buildPostgresRollupQuery(fullBackfill bool) string {
	whereClause := ""
	if !fullBackfill {
		whereClause = "WHERE sd.create_dt > NOW() - INTERVAL '25 hours'"
	}

	return fmt.Sprintf(`
		INSERT INTO sensor_data_hourly (sensor_id, bucket, min_val, max_val, avg_val, sample_count)
		SELECT
			sd.sensor_id,
			date_trunc('hour', sd.create_dt) AS bucket,
			MIN(sd.value),
			MAX(sd.value),
			AVG(sd.value),
			COUNT(*)
		FROM sensor_data sd
		%s
		GROUP BY sd.sensor_id, date_trunc('hour', sd.create_dt)
		ON CONFLICT (sensor_id, bucket) DO UPDATE SET
			min_val      = EXCLUDED.min_val,
			max_val      = EXCLUDED.max_val,
			avg_val      = EXCLUDED.avg_val,
			sample_count = EXCLUDED.sample_count
	`, whereClause)
}
