package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadOnlyQuery_AllowsOneSelectAndRestoresConnection(t *testing.T) {
	store := testStore(t)
	store.sqlDB.SetMaxOpenConns(1)

	rows, err := store.ReadOnlyQuery("/* inspection */ SELECT ';' AS punctuation, x'6372657374' AS bytes; -- done")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, ";", rows[0]["punctuation"])
	assert.Equal(t, "crest", rows[0]["bytes"])

	queryOnlyRows, err := store.ReadOnlyQuery("SELECT query_only FROM pragma_query_only")
	require.NoError(t, err)
	require.Len(t, queryOnlyRows, 1)
	assert.EqualValues(t, 1, queryOnlyRows[0]["query_only"], "inspection must run with SQLite query_only enabled")

	var queryOnly int
	require.NoError(t, store.sqlDB.QueryRow("PRAGMA query_only").Scan(&queryOnly))
	assert.Zero(t, queryOnly, "the pooled connection must be restored for normal Store writes")
	require.NoError(t, store.CreateJob("after-inspection", "test", 1))
}

func TestReadOnlyQuery_RejectsUnsafeOrAmbiguousInput(t *testing.T) {
	store := testStore(t)
	require.NoError(t, store.CreateJob("preserved", "test", 1))

	tests := []struct {
		name    string
		query   string
		message string
	}{
		{name: "empty", query: "", message: "only SELECT"},
		{name: "comment only", query: "/* SELECT */", message: "only SELECT"},
		{name: "write", query: "DELETE FROM jobs", message: "only SELECT"},
		{name: "keyword prefix", query: "SELECTED 1", message: "only SELECT"},
		{name: "two selects", query: "SELECT 1; SELECT 2", message: "exactly one"},
		{name: "chained write", query: "SELECT 1; /* hidden ; */ DELETE FROM jobs", message: "exactly one"},
		{name: "double terminator", query: "SELECT 1;;", message: "exactly one"},
		{name: "unterminated string", query: "SELECT 'value", message: "unterminated"},
		{name: "unterminated comment", query: "SELECT 1 /* value", message: "unterminated"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := store.ReadOnlyQuery(test.query)
			require.ErrorContains(t, err, test.message)
		})
	}

	job, err := store.GetJob("preserved")
	require.NoError(t, err)
	assert.Equal(t, "running", job.Status, "rejected statement chains must not mutate state")
}

func TestReadOnlyQuery_EnforcesRowLimit(t *testing.T) {
	store := testStore(t)
	require.NoError(t, createInspectionRows(store, readOnlyQueryMaxRows+1))

	rows, err := store.ReadOnlyQuery("SELECT value FROM inspection_rows ORDER BY value")
	require.ErrorContains(t, err, "maximum of 1000 rows")
	assert.Nil(t, rows, "bounded inspection must not return an apparently complete partial result")
}

func TestReadOnlyQuery_EnforcesByteLimit(t *testing.T) {
	store := testStore(t)
	_, err := store.sqlDB.Exec("CREATE TABLE inspection_payloads (payload TEXT NOT NULL)")
	require.NoError(t, err)
	_, err = store.sqlDB.Exec(
		"INSERT INTO inspection_payloads (payload) VALUES (?), (?)",
		strings.Repeat("x", 3<<20),
		strings.Repeat("y", 3<<20),
	)
	require.NoError(t, err)

	rows, err := store.ReadOnlyQuery("SELECT payload FROM inspection_payloads")
	require.ErrorContains(t, err, "maximum of 4194304 bytes")
	assert.Nil(t, rows, "bounded inspection must not return an apparently complete partial result")
}

func TestReadOnlyQuery_EnforcesSQLiteValueLimitBeforeScanning(t *testing.T) {
	store := testStore(t)
	store.sqlDB.SetMaxOpenConns(1)

	rows, err := store.ReadOnlyQuery("SELECT zeroblob(4194305) AS oversized")
	require.Error(t, err)
	assert.Nil(t, rows, "SQLite must reject a single oversized value before returning it to Go")

	var length int
	require.NoError(t, store.sqlDB.QueryRow("SELECT length(zeroblob(4194305))").Scan(&length))
	assert.Equal(t, 4194305, length, "the pooled connection's original SQLite value limit must be restored")
}

func TestReadOnlyQuery_EnforcesSQLLengthLimit(t *testing.T) {
	store := testStore(t)
	query := "SELECT 1" + strings.Repeat(" ", readOnlyQueryMaxSQL)

	_, err := store.ReadOnlyQuery(query)
	require.ErrorContains(t, err, "maximum SQL length")
}

func TestReadOnlyQueryContext_PropagatesCancellation(t *testing.T) {
	store := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.ReadOnlyQueryContext(ctx, "SELECT 1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "cancellation should remain discoverable through wrapped errors: %v", err)
}

func TestReadOnlyQueryContext_RejectsNilContext(t *testing.T) {
	store := testStore(t)

	_, err := store.ReadOnlyQueryContext(nil, "SELECT 1")
	require.ErrorContains(t, err, "context is required")
}

func createInspectionRows(store *Store, count int) error {
	if _, err := store.sqlDB.Exec("CREATE TABLE inspection_rows (value INTEGER NOT NULL)"); err != nil {
		return err
	}
	tx, err := store.sqlDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.Prepare("INSERT INTO inspection_rows (value) VALUES (?)")
	if err != nil {
		return err
	}
	defer statement.Close()
	for value := range count {
		if _, err := statement.Exec(value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
