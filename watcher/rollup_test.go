package watcher

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"isley/tests/testutil"
)

// explainPlan returns SQLite's EXPLAIN QUERY PLAN detail lines for query,
// one per line, so tests can assert on how a statement will be executed.
func explainPlan(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
	require.NoError(t, err)
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notUsed, &detail))
		sb.WriteString(detail)
		sb.WriteString("\n")
	}
	require.NoError(t, rows.Err())
	return sb.String()
}

// TestSQLiteRollupQuery_IncrementalRangeSeeksCreateDtIndex guards the fix
// for the ~13s write-lock hold: the incremental rollup must range-seek
// idx_sensor_data_create_dt instead of letting the planner full-scan every
// sensor_data row ever recorded (which it does when no ANALYZE statistics
// exist, i.e. whenever sensor retention pruning is off).
func TestSQLiteRollupQuery_IncrementalRangeSeeksCreateDtIndex(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	plan := explainPlan(t, db, buildSQLiteRollupQuery(false))

	assert.Contains(t, plan, "SEARCH sd USING INDEX idx_sensor_data_create_dt",
		"incremental rollup should range-seek the create_dt index; plan was:\n%s", plan)
	assert.NotContains(t, plan, "SCAN sd",
		"incremental rollup must not full-scan sensor_data; plan was:\n%s", plan)
}

// TestSQLiteRollupQuery_FullBackfillDoesNotPinIndex: a full backfill has no
// time filter and legitimately reads every row, so forcing a create_dt
// index walk there would only add per-row lookups.
func TestSQLiteRollupQuery_FullBackfillDoesNotPinIndex(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, buildSQLiteRollupQuery(true), "INDEXED BY")
	assert.Contains(t, buildSQLiteRollupQuery(false), "INDEXED BY "+sqliteRollupIndex)
}

// TestRefreshHourlyRollups_IncrementalFallsBackWhenIndexMissing: INDEXED BY
// is a hard requirement in SQLite (a missing index is an error, not a
// silent scan), so a database that lacks idx_sensor_data_create_dt must
// still roll up correctly via the unpinned fallback rather than failing
// every 10 minutes forever.
func TestRefreshHourlyRollups_IncrementalFallsBackWhenIndexMissing(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	id := seedSensor(t, db, "test", "dev", "temp")

	hourAgo := time.Now().Add(-1 * time.Hour)
	readingsAt(t, db, id, hourAgo, 10)

	w := newTestWatcher(t, db)
	require.NoError(t, w.RefreshHourlyRollups()) // full backfill, so the next run is incremental

	_, err := db.Exec(`DROP INDEX ` + sqliteRollupIndex)
	require.NoError(t, err)

	readingsAt(t, db, id, hourAgo, 30)
	require.NoError(t, w.RefreshHourlyRollups(), "rollup must fall back rather than fail when the index is gone")

	_, maxV, _, n := hourlyBucket(t, db, id, hourAgo.Format("2006-01-02 15:00:00"))
	assert.InDelta(t, 30.0, maxV, 0.0001)
	assert.Equal(t, 2, n, "the fallback run must still pick up the new reading")
}

// readingsAt seeds N raw sensor_data rows for a sensor at a fixed
// timestamp so the rollup query has a deterministic input to aggregate.
func readingsAt(t *testing.T, db *sql.DB, sensorID int, ts time.Time, values ...float64) {
	t.Helper()
	stamp := ts.Format("2006-01-02 15:04:05")
	for _, v := range values {
		res, err := db.Exec(`INSERT INTO sensor_data (sensor_id, value) VALUES ($1, $2)`, sensorID, v)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE sensor_data SET create_dt = $1 WHERE id = $2`, stamp, id)
		require.NoError(t, err)
	}
}

// hourlyBucket returns the (min, max, avg, sample_count) row that the
// rollup produced for the given sensor and bucket. Test code asserts on
// the returned values directly.
func hourlyBucket(t *testing.T, db *sql.DB, sensorID int, bucket string) (minV, maxV, avgV float64, n int) {
	t.Helper()
	err := db.QueryRow(
		`SELECT min_val, max_val, avg_val, sample_count FROM sensor_data_hourly WHERE sensor_id = $1 AND bucket = $2`,
		sensorID, bucket,
	).Scan(&minV, &maxV, &avgV, &n)
	require.NoError(t, err, "no rollup row for sensor=%d bucket=%s", sensorID, bucket)
	return
}

// TestRefreshHourlyRollups_FullBackfillEmptyTable verifies that an
// empty rollup table triggers a full backfill: every hourly bucket
// present in sensor_data ends up in sensor_data_hourly.
func TestRefreshHourlyRollups_FullBackfillEmptyTable(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	id := seedSensor(t, db, "test", "dev", "temp")

	// Three buckets across two days, including ones older than the
	// 25-hour incremental window — the full backfill should still
	// pick them up.
	readingsAt(t, db, id, time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC), 10, 12)
	readingsAt(t, db, id, time.Date(2026, 1, 1, 11, 15, 0, 0, time.UTC), 14)
	readingsAt(t, db, id, time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC), 20, 22, 24)

	w := newTestWatcher(t, db)
	require.NoError(t, w.RefreshHourlyRollups())

	var rolled int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sensor_data_hourly WHERE sensor_id = $1`, id).Scan(&rolled))
	assert.Equal(t, 3, rolled, "expected one rollup row per distinct (sensor, hour) bucket")

	minV, maxV, avgV, n := hourlyBucket(t, db, id, "2026-01-01 10:00:00")
	assert.InDelta(t, 10.0, minV, 0.0001)
	assert.InDelta(t, 12.0, maxV, 0.0001)
	assert.InDelta(t, 11.0, avgV, 0.0001)
	assert.Equal(t, 2, n)

	minV, maxV, avgV, n = hourlyBucket(t, db, id, "2026-01-05 08:00:00")
	assert.InDelta(t, 20.0, minV, 0.0001)
	assert.InDelta(t, 24.0, maxV, 0.0001)
	assert.InDelta(t, 22.0, avgV, 0.0001)
	assert.Equal(t, 3, n)
}

// TestRefreshHourlyRollups_IncrementalSkipsOldData verifies that a
// non-empty rollup table only re-aggregates the last 25 hours. Old
// rollup rows persist; old raw data outside the window is ignored.
func TestRefreshHourlyRollups_IncrementalSkipsOldData(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	id := seedSensor(t, db, "test", "dev", "temp")

	now := time.Now()

	// Pre-populate the rollup table with a frozen-in-time row that the
	// incremental run must not touch (it's outside the 25-hour window).
	frozenBucket := now.Add(-30 * time.Hour).Format("2006-01-02 15:00:00")
	_, err := db.Exec(
		`INSERT INTO sensor_data_hourly (sensor_id, bucket, min_val, max_val, avg_val, sample_count)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, frozenBucket, 99.0, 99.0, 99.0, 1,
	)
	require.NoError(t, err)

	// Add raw data inside the 25-hour window.
	recent := now.Add(-2 * time.Hour)
	readingsAt(t, db, id, recent, 10, 20, 30)

	w := newTestWatcher(t, db)
	require.NoError(t, w.RefreshHourlyRollups())

	// The frozen out-of-window row should be untouched.
	minV, _, _, _ := hourlyBucket(t, db, id, frozenBucket)
	assert.InDelta(t, 99.0, minV, 0.0001, "out-of-window rollup row should be untouched")

	// The new in-window bucket should reflect the seeded readings.
	recentBucket := recent.Format("2006-01-02 15:00:00")
	minV, maxV, avgV, n := hourlyBucket(t, db, id, recentBucket)
	assert.InDelta(t, 10.0, minV, 0.0001)
	assert.InDelta(t, 30.0, maxV, 0.0001)
	assert.InDelta(t, 20.0, avgV, 0.0001)
	assert.Equal(t, 3, n)
}

// TestRefreshHourlyRollups_IncrementalUpdatesInWindowBucket asserts the
// UPSERT semantics: when a rollup bucket already exists and new raw
// data arrives within the 25-hour window, the existing row is updated
// rather than producing a duplicate.
func TestRefreshHourlyRollups_IncrementalUpdatesInWindowBucket(t *testing.T) {
	t.Parallel()

	db := testutil.NewTestDB(t)
	id := seedSensor(t, db, "test", "dev", "temp")

	// First pass: one reading in a recent hour.
	now := time.Now()
	hourAgo := now.Add(-1 * time.Hour)
	readingsAt(t, db, id, hourAgo, 10)

	w := newTestWatcher(t, db)
	require.NoError(t, w.RefreshHourlyRollups()) // full backfill

	bucketKey := hourAgo.Format("2006-01-02 15:00:00")
	minV, maxV, _, n := hourlyBucket(t, db, id, bucketKey)
	require.InDelta(t, 10.0, minV, 0.0001)
	require.InDelta(t, 10.0, maxV, 0.0001)
	require.Equal(t, 1, n)

	// Second pass: another reading in the same hour. The next refresh
	// should see all three readings in that bucket and update the row
	// in place.
	readingsAt(t, db, id, hourAgo, 30)
	require.NoError(t, w.RefreshHourlyRollups()) // incremental

	minV, maxV, _, n = hourlyBucket(t, db, id, bucketKey)
	assert.InDelta(t, 10.0, minV, 0.0001, "min stays at 10")
	assert.InDelta(t, 30.0, maxV, 0.0001, "max bumps to 30")
	assert.Equal(t, 2, n, "sample_count should reflect the combined readings")

	// Confirm we still only have one row for that bucket — UPSERT, not append.
	var rowsForBucket int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sensor_data_hourly WHERE sensor_id = $1 AND bucket = $2`, id, bucketKey).Scan(&rowsForBucket))
	assert.Equal(t, 1, rowsForBucket)
}
