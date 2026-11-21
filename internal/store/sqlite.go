// SPDX-License-Identifier: Apache-2.0

// Package store owns the SQLite database: opening it, migrations, queries,
// and full-text search.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	// The blank import registers the pure-Go "sqlite" driver with
	// database/sql (D-14). Nothing else in the program touches the driver
	// package directly.
	_ "modernc.org/sqlite"
)

// pragmas are applied to every new connection in the pool. Pragmas are
// per-connection in SQLite, so they go in the DSN rather than being run
// once after Open.
var pragmas = []string{
	"journal_mode(WAL)",   // readers don't block the writer
	"foreign_keys(1)",     // off by default in SQLite
	"busy_timeout(5000)",  // wait up to 5s for a lock instead of failing
	"synchronous(NORMAL)", // safe with WAL; much faster than FULL
}

// DBFile is the database file name inside the data directory.
const DBFile = "grimoire.db"

// OpenDataDir creates dataDir if needed and opens the database in it.
func OpenDataDir(ctx context.Context, dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	return Open(ctx, filepath.Join(dataDir, DBFile))
}

// Open opens (creating if needed) the database file at path and checks the
// connection.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	// Transactions take the write lock when they begin (BEGIN IMMEDIATE).
	// A deferred transaction that reads and then writes can fail with
	// SQLITE_BUSY when it tries to upgrade its lock, and busy_timeout does
	// not retry that case; IMMEDIATE waits for the lock instead.
	q.Set("_txlock", "immediate")
	// "file:" URI form lets us pass the pragmas as query parameters.
	dsn := "file:" + path + "?" + q.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}
