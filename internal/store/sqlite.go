package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"github.com/crestenstclair/crest-spec/internal/db"
	_ "modernc.org/sqlite"
)

const sqliteBusyTimeoutMS = 5000

var memoryDatabaseSequence atomic.Uint64

// New opens, configures, and migrates a SQLite database. Connection-scoped
// settings are encoded in the modernc DSN so database/sql applies them to
// every physical connection in its pool, not only the first one it opens.
func New(dbPath string) (*Store, error) {
	sqlDB, err := openSQLite(dbPath)
	if err != nil {
		return nil, err
	}

	s := &Store{
		sqlDB:   sqlDB,
		queries: db.New(sqlDB),
	}
	if err := s.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func openSQLite(dbPath string) (*sql.DB, error) {
	dsn := sqliteDSN(dbPath)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Force the driver to create and configure a connection now. Without a
	// ping, malformed DSNs and unsupported PRAGMAs fail later during migration.
	if err := sqlDB.PingContext(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("initialize db: %w", err)
	}
	return sqlDB, nil
}

func sqliteDSN(dbPath string) string {
	if dbPath == ":memory:" {
		// A bare :memory: database is private to one physical connection. Give
		// each Store a unique shared-memory URI so pooled connections see the
		// same schema without leaking state between Store instances.
		dbPath = fmt.Sprintf(
			"file:crest_spec_memory_%d_%d?mode=memory&cache=shared",
			os.Getpid(),
			memoryDatabaseSequence.Add(1),
		)
	}

	separator := "?"
	if strings.Contains(dbPath, "?") {
		separator = "&"
	}
	pragmas := []string{
		"busy_timeout(" + fmt.Sprint(sqliteBusyTimeoutMS) + ")",
		"foreign_keys(1)",
		"journal_mode(WAL)",
	}
	values := make([]string, 0, len(pragmas))
	for _, pragma := range pragmas {
		values = append(values, "_pragma="+url.QueryEscape(pragma))
	}
	return dbPath + separator + strings.Join(values, "&")
}
