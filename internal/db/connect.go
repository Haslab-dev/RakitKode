// Package db connects to the project SQLite database and runs migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

var pragmas = map[string]string{
	"foreign_keys": "ON",
	"journal_mode": "WAL",
	"page_size":    "4096",
	"temp_store":   "MEMORY",
	"cache_size":   "-8000",
	"synchronous":  "NORMAL",
	"busy_timeout": "30000",
}

//go:embed migrations/*.sql
var FS embed.FS

func init() {
	goose.SetBaseFS(FS)
	goose.SetLogger(goose.NopLogger())
}

func openDB(dbPath string) (*sql.DB, error) {
	params := url.Values{}
	for name, value := range pragmas {
		params.Add("_pragma", fmt.Sprintf("%s(%s)", name, value))
	}
	params.Set("_txlock", "immediate")
	dsn := fmt.Sprintf("file:%s?%s", dbPath, params.Encode())
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	// Serialize all access through a single connection. SQLite
	// serializes writes at the file level anyway.
	conn.SetMaxOpenConns(1)
	return conn, nil
}

// Connect opens the SQLite database in dataDir and runs migrations.
func Connect(ctx context.Context, dataDir string) (*sql.DB, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("data dir is not set")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create data directory %q: %w", dataDir, err)
	}
	dbPath := filepath.Join(dataDir, "rakitkode.db")
	conn, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	goose.SetDialect("sqlite3")
	if err := goose.UpContext(ctx, conn, "migrations"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	return conn, nil
}
