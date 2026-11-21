// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// CheckResult reports what Check verified.
type CheckResult struct {
	SQLiteVersion string `json:"sqlite_version"`
	JournalMode   string `json:"journal_mode"`
	FTS5          bool   `json:"fts5"`
	BM25          bool   `json:"bm25"`
	VacuumInto    bool   `json:"vacuum_into"`
}

// Check exercises the SQLite features the application depends on (spike S1):
// WAL, FTS5 with the unicode61 tokenizer and prefix indexes, bm25 ranking,
// and VACUUM INTO for consistent backups. It works in a fresh temporary
// directory under dir and removes it afterwards.
func Check(ctx context.Context, dir string) (CheckResult, error) {
	var res CheckResult

	tmp, err := os.MkdirTemp(dir, "grimoire-check-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(tmp)

	db, err := Open(ctx, filepath.Join(tmp, "check.db"))
	if err != nil {
		return res, err
	}
	defer db.Close()

	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&res.SQLiteVersion); err != nil {
		return res, fmt.Errorf("sqlite_version: %w", err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&res.JournalMode); err != nil {
		return res, fmt.Errorf("journal_mode: %w", err)
	}
	if res.JournalMode != "wal" {
		return res, fmt.Errorf("journal_mode is %q, want wal", res.JournalMode)
	}

	// Same shape as recipes_fts in docs/DESIGN.md §10.
	stmts := []string{
		`CREATE VIRTUAL TABLE fts USING fts5(
			title, ingredients, tags, contributor, body,
			tokenize = 'unicode61 remove_diacritics 2',
			prefix = '2 3'
		)`,
		`INSERT INTO fts VALUES
			('Crème Brûlée', 'cream, sugar, eggs', 'dessert', 'Example Cook', 'Bake in a water bath.'),
			('Buttermilk Biscuits', 'flour, buttermilk, butter', 'bread', 'Another Cook', 'Cut in the butter.'),
			('Pancakes', 'flour, milk, eggs', 'breakfast', 'Example Cook', 'Use buttermilk if you have it.')`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return res, fmt.Errorf("fts5: %w", err)
		}
	}

	// Diacritics are folded ("creme" finds "Crème") and prefixes match.
	var title string
	if err := db.QueryRowContext(ctx, `SELECT title FROM fts WHERE fts MATCH 'creme brul*'`).Scan(&title); err != nil {
		return res, fmt.Errorf("fts5 query: %w", err)
	}
	res.FTS5 = title == "Crème Brûlée"
	if !res.FTS5 {
		return res, fmt.Errorf("fts5 query returned %q", title)
	}

	// The title column is weighted highest, so a title match outranks a
	// body-only match. bm25 scores are negative; lower is better.
	rows, err := db.QueryContext(ctx, `
		SELECT title FROM fts WHERE fts MATCH 'buttermilk'
		ORDER BY bm25(fts, 10.0, 5.0, 5.0, 3.0, 1.0)`)
	if err != nil {
		return res, fmt.Errorf("bm25: %w", err)
	}
	var ranked []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return res, err
		}
		ranked = append(ranked, t)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return res, fmt.Errorf("bm25: %w", err)
	}
	res.BM25 = len(ranked) == 2 && ranked[0] == "Buttermilk Biscuits"
	if !res.BM25 {
		return res, fmt.Errorf("bm25 ranking = %q", ranked)
	}

	// VACUUM INTO writes a consistent snapshot to a new file (§14.4).
	snap := filepath.Join(tmp, "snapshot.db")
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snap); err != nil {
		return res, fmt.Errorf("vacuum into: %w", err)
	}
	sdb, err := sql.Open("sqlite", "file:"+snap+"?mode=ro")
	if err != nil {
		return res, err
	}
	defer sdb.Close()
	var n int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM fts").Scan(&n); err != nil {
		return res, fmt.Errorf("read snapshot: %w", err)
	}
	res.VacuumInto = n == 3
	if !res.VacuumInto {
		return res, fmt.Errorf("snapshot has %d rows, want 3", n)
	}
	return res, nil
}
