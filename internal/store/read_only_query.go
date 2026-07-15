package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	readOnlyQueryTimeout  = 5 * time.Second
	readOnlyQueryMaxBytes = 4 << 20
	readOnlyQueryMaxRows  = 1000
	readOnlyQueryMaxSQL   = 64 << 10
)

// ReadOnlyQuery is the compatibility entry point for callers that do not have
// a context. Inspection queries still receive the default execution timeout.
func (s *Store) ReadOnlyQuery(query string) ([]map[string]interface{}, error) {
	return s.ReadOnlyQueryContext(context.Background(), query)
}

// ReadOnlyQueryContext executes one bounded SELECT on a connection placed in
// SQLite query-only mode. The query-only setting is authoritative protection
// against writes; lexical validation provides a clear API error and rejects
// statement chaining before SQL reaches the driver.
func (s *Store) ReadOnlyQueryContext(ctx context.Context, query string) (results []map[string]interface{}, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("query context is required")
	}
	if err := validateReadOnlyStatement(query); err != nil {
		return nil, err
	}

	queryCtx, cancel := context.WithTimeout(ctx, readOnlyQueryTimeout)
	defer cancel()

	conn, err := s.sqlDB.Conn(queryCtx)
	if err != nil {
		return nil, fmt.Errorf("acquire query connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(queryCtx, "PRAGMA query_only=ON"); err != nil {
		return nil, fmt.Errorf("enable query-only mode: %w", err)
	}
	// query_only is connection scoped. Always restore it before returning the
	// connection to the general-purpose pool, including after cancellation.
	defer func() {
		resetCtx, resetCancel := context.WithTimeout(context.Background(), time.Second)
		defer resetCancel()
		if _, resetErr := conn.ExecContext(resetCtx, "PRAGMA query_only=OFF"); resetErr != nil {
			// Never return a connection with query_only still enabled to the
			// general Store pool. driver.ErrBadConn makes database/sql discard it.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			if err == nil {
				results = nil
				err = fmt.Errorf("disable query-only mode: %w", resetErr)
			}
		}
	}()
	originalLengthLimit, err := moderncsqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, readOnlyQueryMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("limit query value size: %w", err)
	}
	defer func() {
		if _, resetErr := moderncsqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, originalLengthLimit); resetErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			if err == nil {
				results = nil
				err = fmt.Errorf("restore query value limit: %w", resetErr)
			}
		}
	}()

	rows, err := conn.QueryContext(queryCtx, query)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	return collectReadOnlyRows(rows)
}

func collectReadOnlyRows(rows *sql.Rows) ([]map[string]interface{}, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}

	resultBytes := 0
	var results []map[string]interface{}
	for rows.Next() {
		if len(results) == readOnlyQueryMaxRows {
			return nil, fmt.Errorf("query result exceeds maximum of %d rows", readOnlyQueryMaxRows)
		}

		values := make([]interface{}, len(columns))
		pointers := make([]interface{}, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		row := make(map[string]interface{}, len(columns))
		for i, column := range columns {
			value := values[i]
			if bytes, ok := value.([]byte); ok {
				value = string(bytes)
			}
			resultBytes += len(column) + readOnlyValueSize(value)
			if resultBytes > readOnlyQueryMaxBytes {
				return nil, fmt.Errorf("query result exceeds maximum of %d bytes", readOnlyQueryMaxBytes)
			}
			row[column] = value
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return results, nil
}

func readOnlyValueSize(value interface{}) int {
	switch value := value.(type) {
	case nil:
		return 0
	case string:
		return len(value)
	case []byte:
		return len(value)
	case int64, float64, bool, time.Time:
		return 8
	default:
		return len(fmt.Sprint(value))
	}
}
