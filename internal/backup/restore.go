// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// ErrNotEmpty means Restore was pointed at a data directory that already
// holds something, without force.
var ErrNotEmpty = errors.New("backup: data directory is not empty (use force to replace its database and images)")

// ignoredEntries don't make a data directory "non-empty". A freshly
// formatted ext4 volume has lost+found at its root.
var ignoredEntries = []string{"lost+found"}

// dbSidecars are SQLite's companion files. A stale -wal left next to a
// restored database would be replayed into it and corrupt it, so they are
// moved aside together with the database.
var dbSidecars = []string{DBName + "-wal", DBName + "-shm", DBName + "-journal"}

// Restore unpacks a backup from r into dataDir. The server must not be
// running against dataDir.
//
// It refuses a non-empty dataDir unless force is set; with force it
// replaces grimoire.db (and its -wal/-shm files) and images/, and leaves
// everything else alone. The archive is extracted into a staging directory
// inside dataDir and fully verified (checksums, file list, SQLite integrity
// check) before anything existing is touched; the final swap is a few
// renames on the same filesystem. On any error dataDir is left as it was.
func Restore(ctx context.Context, r io.Reader, dataDir string, force bool) (Manifest, error) {
	return restore(ctx, r, dataDir, force, DefaultLimits)
}

func restore(ctx context.Context, r io.Reader, dataDir string, force bool, lim Limits) (m Manifest, err error) {
	created, err := prepareDataDir(dataDir, force)
	if err != nil {
		return m, err
	}
	if created {
		// We made dataDir, so "as it was" means gone again.
		defer func() {
			if err != nil {
				_ = os.RemoveAll(dataDir)
			}
		}()
	}

	// The leading dot keeps the staging dir out of the way; being inside
	// dataDir puts it on the same filesystem, so the final moves are renames.
	staging, err := os.MkdirTemp(dataDir, ".restore-")
	if err != nil {
		return m, fmt.Errorf("backup: staging dir: %w", err)
	}
	defer os.RemoveAll(staging)

	m, err = readArchive(ctxReader{ctx, r}, lim, func(name string) (io.WriteCloser, error) {
		return createStaged(staging, name)
	})
	if err != nil {
		return m, err
	}
	if err := integrityCheck(ctx, filepath.Join(staging, DBName)); err != nil {
		return m, err
	}
	// A backup without images still restores an (empty) images directory.
	if err := os.MkdirAll(filepath.Join(staging, ImagesDir), 0o750); err != nil {
		return m, err
	}
	return m, swapIn(staging, dataDir)
}

// prepareDataDir creates dataDir if needed (reporting whether it did) and
// enforces the empty-unless-force rule.
func prepareDataDir(dataDir string, force bool) (created bool, err error) {
	entries, err := os.ReadDir(dataDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dataDir, 0o750); err != nil {
			return false, fmt.Errorf("backup: create data dir: %w", err)
		}
		return true, nil
	case err != nil:
		return false, fmt.Errorf("backup: data dir: %w", err)
	}
	for _, e := range entries {
		if !slices.Contains(ignoredEntries, e.Name()) && !force {
			return false, fmt.Errorf("%w: %s contains %q", ErrNotEmpty, dataDir, e.Name())
		}
	}
	return false, nil
}

// createStaged creates the file for archive member name under staging.
// readArchive has already validated name as a clean relative path.
func createStaged(staging, name string) (io.WriteCloser, error) {
	p := filepath.Join(staging, filepath.FromSlash(name))
	// Belt and braces: the joined path must still be inside staging.
	if rel, err := filepath.Rel(staging, p); err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("backup: unsafe path %q", name)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return nil, err
	}
	// O_EXCL: never write through something that already exists.
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, err
	}
	return syncCloser{f}, nil
}

// syncCloser flushes a file to disk before closing it, so a crash right
// after the restore can't leave a half-written database behind.
type syncCloser struct{ *os.File }

func (s syncCloser) Close() error {
	if err := s.File.Sync(); err != nil {
		_ = s.File.Close()
		return err
	}
	return s.File.Close()
}

// integrityCheck opens the staged database read-only and runs SQLite's own
// consistency check, so a database that matches its checksum but was bad
// when backed up is caught before it replaces anything.
func integrityCheck(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("backup: open restored database: %w", err)
	}
	defer db.Close()
	var res string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res); err != nil {
		return fmt.Errorf("backup: integrity check: %w", err)
	}
	if res != "ok" {
		return fmt.Errorf("backup: integrity check failed: %s", res)
	}
	return db.Close()
}

// swapIn moves the staged database and images into dataDir. Anything they
// replace is first moved into staging/.old (deleted with staging on
// return); if a step fails, completed moves are undone in reverse.
func swapIn(staging, dataDir string) error {
	old := filepath.Join(staging, ".old")
	if err := os.Mkdir(old, 0o750); err != nil {
		return err
	}
	type move struct{ from, to string }
	var done []move
	mv := func(from, to string) error {
		if err := os.Rename(from, to); err != nil {
			return err
		}
		done = append(done, move{from, to})
		return nil
	}
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			_ = os.Rename(done[i].to, done[i].from)
		}
	}

	// 1. Move the existing database, its sidecars, and images aside.
	for _, name := range append([]string{DBName, ImagesDir}, dbSidecars...) {
		p := filepath.Join(dataDir, name)
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err := mv(p, filepath.Join(old, name)); err != nil {
			undo()
			return fmt.Errorf("backup: move aside %s: %w", name, err)
		}
	}
	// 2. Move the new ones in.
	for _, name := range []string{DBName, ImagesDir} {
		if err := mv(filepath.Join(staging, name), filepath.Join(dataDir, name)); err != nil {
			undo()
			return fmt.Errorf("backup: move %s into place: %w", name, err)
		}
	}
	// Persist the renames themselves (best effort; not all platforms can
	// sync a directory).
	if d, err := os.Open(dataDir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// ctxReader stops reading once ctx is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Prune deletes the oldest backups (grimoire-*.tar.gz) in dir so that at
// most keep remain. The names embed a UTC timestamp, so name order is age
// order. Other files are never touched.
func Prune(dir string, keep int) (removed []string, err error) {
	if keep < 1 {
		return nil, fmt.Errorf("backup: keep must be at least 1, got %d", keep)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "grimoire-*.tar.gz"))
	if err != nil {
		return nil, err
	}
	var backups []string
	for _, p := range matches {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			backups = append(backups, p)
		}
	}
	slices.Sort(backups)
	for len(backups) > keep {
		if err := os.Remove(backups[0]); err != nil {
			return removed, err
		}
		removed = append(removed, backups[0])
		backups = backups[1:]
	}
	return removed, nil
}

// FileName is the name for a backup taken at t, e.g.
// grimoire-20261120T031500Z.tar.gz. Prune relies on this shape.
func FileName(t time.Time) string {
	return "grimoire-" + t.UTC().Format("20060102T150405Z") + ".tar.gz"
}
