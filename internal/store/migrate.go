// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Migrations are plain SQL files named NNNN_description.sql, applied in
// order, each in its own transaction. An applied migration must never be
// edited: add a new one instead. The runner stores each file's checksum and
// refuses to run if an applied file has changed.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration is one embedded migration file.
type Migration struct {
	Version  int
	Name     string // "init" for 0001_init.sql
	SQL      string
	Checksum string // hex SHA-256 of SQL
}

// MigrationStatus describes a known migration and whether it has been applied.
type MigrationStatus struct {
	Migration
	AppliedAt string // "" if pending
}

var (
	// ErrMigrationChanged means an applied migration's file differs from
	// what was applied. Editing applied migrations is never allowed.
	ErrMigrationChanged = errors.New("applied migration has changed")
	// ErrDatabaseNewer means the database has migrations this binary does
	// not know: it was migrated by a newer version. Running an older binary
	// against it could corrupt data, so we stop.
	ErrDatabaseNewer = errors.New("database was migrated by a newer version")
)

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// Migrations returns the embedded migrations in order.
func Migrations() ([]Migration, error) {
	return loadMigrations(migrationFiles, "migrations")
}

func loadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must look like 0001_description.sql", e.Name())
		}
		body, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, err
		}
		v, _ := strconv.Atoi(m[1])
		sum := sha256.Sum256(body)
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1, 2, 3, ... without gaps; found %04d at position %d", m.Version, i+1)
		}
	}
	return out, nil
}

const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    checksum   TEXT NOT NULL,
    applied_at TEXT NOT NULL
) STRICT`

// Migrate applies every pending migration and returns the ones it applied.
// It is safe to call on every start-up: with nothing pending it only checks
// that the applied migrations still match the embedded files.
func Migrate(ctx context.Context, db *sql.DB) ([]Migration, error) {
	return migrate(ctx, db, migrationFiles, "migrations")
}

func migrate(ctx context.Context, db *sql.DB, fsys fs.FS, dir string) ([]Migration, error) {
	known, err := loadMigrations(fsys, dir)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, createMigrationsTable); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := verifyApplied(known, applied); err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range known {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			return done, err
		}
		done = append(done, m)
	}

	// Belt and braces: a migration must not leave dangling references.
	if len(done) > 0 {
		if err := foreignKeyCheck(ctx, db); err != nil {
			return done, err
		}
	}
	return done, nil
}

type appliedRow struct {
	name, checksum, appliedAt string
}

func appliedMigrations(ctx context.Context, db *sql.DB) (map[int]appliedRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[int]appliedRow{}
	for rows.Next() {
		var v int
		var r appliedRow
		if err := rows.Scan(&v, &r.name, &r.checksum, &r.appliedAt); err != nil {
			return nil, err
		}
		out[v] = r
	}
	return out, rows.Err()
}

func verifyApplied(known []Migration, applied map[int]appliedRow) error {
	byVersion := map[int]Migration{}
	for _, m := range known {
		byVersion[m.Version] = m
	}
	for v, r := range applied {
		m, ok := byVersion[v]
		if !ok {
			return fmt.Errorf("%w: it has migration %04d_%s, the newest this binary knows is %04d",
				ErrDatabaseNewer, v, r.name, len(known))
		}
		if m.Checksum != r.checksum {
			return fmt.Errorf("%w: %04d_%s.sql no longer matches what was applied on %s; add a new migration instead of editing it",
				ErrMigrationChanged, v, m.Name, r.appliedAt)
		}
	}
	return nil
}

// applyOne runs a migration and records it in one transaction, so a
// failure leaves the database exactly as it was before.
func applyOne(ctx context.Context, db *sql.DB, m Migration) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Roll back unless we reach Commit. Rollback after Commit is a no-op.
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("migration %04d_%s: %w", m.Version, m.Name, err)
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`,
		m.Version, m.Name, m.Checksum, Now()); err != nil {
		return fmt.Errorf("record migration %04d: %w", m.Version, err)
	}
	return tx.Commit()
}

func foreignKeyCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		_ = rows.Scan(&table, &rowid, &parent, &fkid)
		return fmt.Errorf("foreign key check failed: %s row %v references missing %s", table, rowid.Int64, parent)
	}
	return rows.Err()
}

// Status lists every known migration and when it was applied.
func Status(ctx context.Context, db *sql.DB) ([]MigrationStatus, error) {
	known, err := Migrations()
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, createMigrationsTable); err != nil {
		return nil, err
	}
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return nil, err
	}
	if err := verifyApplied(known, applied); err != nil {
		return nil, err
	}
	out := make([]MigrationStatus, len(known))
	for i, m := range known {
		out[i] = MigrationStatus{Migration: m, AppliedAt: applied[m.Version].appliedAt}
	}
	return out, nil
}

// Now is the timestamp format used in every table: UTC RFC 3339 with
// milliseconds. Fixed-width text sorts chronologically.
func Now() string {
	return FormatTime(time.Now())
}

// FormatTime formats t like Now.
func FormatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
