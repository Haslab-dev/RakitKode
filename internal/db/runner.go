package db

import (
	"context"
	"database/sql"
	"log"
)

// runner executes queries against the shared handle. The *sql.DB is
// configured with MaxOpenConns(1), so all statements serialize.
var handle *sql.DB

// SetHandle installs the database handle used by Queries. Call once
// after Connect.
func SetHandle(conn *sql.DB) { handle = conn }

func exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if handle == nil {
		return nil, sql.ErrConnDone
	}
	return handle.ExecContext(ctx, query, args...)
}

func query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if handle == nil {
		return nil, sql.ErrConnDone
	}
	return handle.QueryContext(ctx, query, args...)
}

func queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if handle == nil {
		log.Print("db: query before SetHandle")
		return nil
	}
	return handle.QueryRowContext(ctx, query, args...)
}
