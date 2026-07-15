package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ConfiguresEveryPooledConnection(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "pool.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	store.sqlDB.SetMaxOpenConns(4)

	connections := make([]*sql.Conn, 0, 4)
	for range 4 {
		connection, connErr := store.sqlDB.Conn(context.Background())
		require.NoError(t, connErr)
		connections = append(connections, connection)
	}
	t.Cleanup(func() {
		for _, connection := range connections {
			require.NoError(t, connection.Close())
		}
	})

	for index, connection := range connections {
		var foreignKeys int
		require.NoError(t, connection.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "connection %d must enforce foreign keys", index)

		var busyTimeout int
		require.NoError(t, connection.QueryRowContext(context.Background(), "PRAGMA busy_timeout").Scan(&busyTimeout))
		assert.Equal(t, sqliteBusyTimeoutMS, busyTimeout, "connection %d must use the configured busy timeout", index)

		var journalMode string
		require.NoError(t, connection.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&journalMode))
		assert.Equal(t, "wal", journalMode, "connection %d must use WAL", index)

		_, insertErr := connection.ExecContext(
			context.Background(),
			"INSERT INTO generated_files (path, resource_id, content_hash, prompt_hash, model, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			"missing-parent", "missing-resource", "content", "prompt", "model", "2026-01-01T00:00:00Z",
		)
		assert.ErrorContains(t, insertErr, "FOREIGN KEY constraint failed", "connection %d must reject invalid references", index)
	}
}

func TestNew_MemoryDatabaseIsSharedAcrossItsPool(t *testing.T) {
	store := testStore(t)
	store.sqlDB.SetMaxOpenConns(4)

	connections := make([]*sql.Conn, 0, 4)
	for range 4 {
		connection, err := store.sqlDB.Conn(context.Background())
		require.NoError(t, err)
		connections = append(connections, connection)
	}
	defer func() {
		for _, connection := range connections {
			require.NoError(t, connection.Close())
		}
	}()

	for index, connection := range connections {
		var migrationCount int
		require.NoError(t, connection.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount))
		assert.Positive(t, migrationCount, "connection %d must see the migrated shared-memory schema", index)

		var foreignKeys int
		require.NoError(t, connection.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&foreignKeys))
		assert.Equal(t, 1, foreignKeys, "connection %d must enforce foreign keys", index)
	}
}

func TestNew_MemoryStoresAreIsolated(t *testing.T) {
	first := testStore(t)
	second := testStore(t)

	_, err := first.sqlDB.Exec("CREATE TABLE store_isolation_probe (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)

	var count int
	require.NoError(t, first.sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'store_isolation_probe'").Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, second.sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'store_isolation_probe'").Scan(&count))
	assert.Zero(t, count, "separate :memory: Store instances must not leak state")
}
