// SPDX-License-Identifier: Apache-2.0

// Package backup writes, verifies and restores full backups (§14.4).
//
// A backup is a .tar.gz holding, in this order:
//
//	grimoire.db                 a consistent snapshot (SQLite VACUUM INTO)
//	images/<aa>/<bb>/<sha>.<ext> every stored image and thumbnail, laid out
//	                            exactly as under <data dir>/images
//	manifest.json               format, counts, and size + SHA-256 of every
//	                            other file
//
// The manifest comes last because its checksums are computed while the
// files stream past; that lets Write produce the archive in one pass without
// buffering it. Readers check every file against the manifest at the end.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// FormatVersion is the archive layout version written to the manifest.
// Readers refuse versions they don't know.
const FormatVersion = 1

// Names inside the archive.
const (
	DBName       = store.DBFile // "grimoire.db"
	ManifestName = "manifest.json"
	ImagesDir    = "images"
)

// Meta is what the caller knows about the running program.
type Meta struct {
	AppVersion string
	// Now is the backup time; zero means time.Now().
	Now time.Time
}

// Manifest describes a backup. It is stored as manifest.json.
type Manifest struct {
	Format        int    `json:"format"`
	CreatedAt     string `json:"created_at"` // UTC, RFC 3339
	AppVersion    string `json:"app_version"`
	SchemaVersion int    `json:"schema_version"` // highest applied migration
	Counts        Counts `json:"counts"`
	Files         []File `json:"files"` // every file except manifest.json
}

// Counts summarises the backup's contents.
type Counts struct {
	Recipes int `json:"recipes"` // including recipes in the trash
	Images  int `json:"images"`  // full-size images (thumbnails not counted)
}

// File is one archive member's checksum record.
type File struct {
	Path   string `json:"path"` // slash-separated, relative to the archive root
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"` // lowercase hex
}

// Write streams a backup of db and files to w and returns its manifest.
// files may be nil (no images). It is safe to call while the server is
// running: the database snapshot is transactionally consistent, and image
// files are immutable once written (they are named by their content).
//
// If Write fails part-way, w has received a truncated archive; callers
// streaming over HTTP should abort the connection so the client notices.
func Write(ctx context.Context, db *sql.DB, files *imagestore.Store, w io.Writer, meta Meta) (Manifest, error) {
	now := meta.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC().Truncate(time.Second)
	m := Manifest{
		Format:     FormatVersion,
		CreatedAt:  now.Format(time.RFC3339),
		AppVersion: meta.AppVersion,
		Files:      []File{},
	}

	// 1. Snapshot the database into a private temp directory. VACUUM INTO
	// refuses to overwrite, so the target must not exist yet.
	tmpDir, err := os.MkdirTemp(os.TempDir(), "grimoire-backup-")
	if err != nil {
		return m, fmt.Errorf("backup: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	snap := filepath.Join(tmpDir, DBName)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snap); err != nil {
		return m, fmt.Errorf("backup: snapshot database: %w", err)
	}
	if err := prepareSnapshot(ctx, snap, &m); err != nil {
		return m, err
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	add := func(name, src string) error {
		f, err := addFile(tw, name, src, now)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, f)
		return ctx.Err() // stop promptly if the client went away
	}

	// 2. The database.
	if err := add(DBName, snap); err != nil {
		return m, err
	}

	// 3. Images and thumbnails. Walk only reports full-size images at their
	// proper <aa>/<bb>/ place, so stray temp files are never included.
	if files != nil {
		err := files.Walk(func(sha, ext string) error {
			full := files.Path(sha, ext)
			if err := add(storeName(full), full); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil // deleted by image GC after Walk listed it
				}
				return err
			}
			m.Counts.Images++
			thumb := files.ThumbPath(sha)
			if err := add(storeName(thumb), thumb); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		})
		if err != nil {
			return m, fmt.Errorf("backup: images: %w", err)
		}
	}

	// 4. The manifest, last.
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	body = append(body, '\n')
	hdr := &tar.Header{Name: ManifestName, Mode: 0o640, Size: int64(len(body)), ModTime: now, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return m, fmt.Errorf("backup: write manifest: %w", err)
	}
	if _, err := tw.Write(body); err != nil {
		return m, fmt.Errorf("backup: write manifest: %w", err)
	}
	if err := tw.Close(); err != nil {
		return m, fmt.Errorf("backup: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return m, fmt.Errorf("backup: close gzip: %w", err)
	}
	return m, nil
}

// prepareSnapshot makes the snapshot a self-contained single file and reads
// the counts and schema version from it, so they match the snapshot exactly
// even while the live database keeps changing.
func prepareSnapshot(ctx context.Context, snap string, m *Manifest) error {
	// Opened without the store's pragmas: no WAL. journal_mode=DELETE makes
	// sure the file doesn't need a -wal sidecar, whatever mode VACUUM INTO
	// left in its header. The restored server switches it back to WAL.
	sdb, err := sql.Open("sqlite", "file:"+snap)
	if err != nil {
		return fmt.Errorf("backup: open snapshot: %w", err)
	}
	defer sdb.Close()
	if _, err := sdb.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		return fmt.Errorf("backup: snapshot journal mode: %w", err)
	}
	if err := sdb.QueryRowContext(ctx, `SELECT count(*) FROM recipes`).Scan(&m.Counts.Recipes); err != nil {
		return fmt.Errorf("backup: count recipes: %w", err)
	}
	if err := sdb.QueryRowContext(ctx, `SELECT COALESCE(max(version), 0) FROM schema_migrations`).Scan(&m.SchemaVersion); err != nil {
		return fmt.Errorf("backup: schema version: %w", err)
	}
	return sdb.Close()
}

// storeName turns an imagestore file path into its archive name. The image
// store lays files out as <root>/<aa>/<bb>/<file>, so the last three path
// elements are the path relative to the store root.
func storeName(p string) string {
	bb := filepath.Dir(p)
	aa := filepath.Dir(bb)
	return path.Join(ImagesDir, filepath.Base(aa), filepath.Base(bb), filepath.Base(p))
}

// addFile copies the file at src into tw as name, hashing it on the way.
func addFile(tw *tar.Writer, name, src string, mtime time.Time) (File, error) {
	f, err := os.Open(src)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return File{}, err
	}
	hdr := &tar.Header{Name: name, Mode: 0o640, Size: fi.Size(), ModTime: mtime, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return File{}, fmt.Errorf("backup: %s: %w", name, err)
	}
	h := sha256.New()
	// The tar header promised exactly Size bytes; copy no more than that
	// and fail if the file turned out shorter.
	n, err := io.Copy(tw, io.TeeReader(io.LimitReader(f, fi.Size()), h))
	if err != nil {
		return File{}, fmt.Errorf("backup: %s: %w", name, err)
	}
	if n != fi.Size() {
		return File{}, fmt.Errorf("backup: %s: file shrank while reading", name)
	}
	return File{Path: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// ---- reading ----

// Limits bound what a reader accepts, so a hostile or corrupt archive can't
// fill the disk.
type Limits struct {
	MaxFile  int64 // largest single member
	MaxTotal int64 // sum of all members
}

// DefaultLimits are used by Verify and Restore.
var DefaultLimits = Limits{MaxFile: 10 << 30, MaxTotal: 100 << 30} // 10 GiB, 100 GiB

// maxManifest bounds manifest.json, which is read into memory.
const maxManifest = 64 << 20

// Verify reads a whole archive and checks it against its manifest: every
// listed file present with the right size and SHA-256, nothing unlisted, the
// manifest last, and the gzip stream intact. It writes nothing to disk.
func Verify(r io.Reader) (Manifest, error) {
	return readArchive(r, DefaultLimits, func(string) (io.WriteCloser, error) {
		return nopWriteCloser{io.Discard}, nil
	})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// readArchive walks the archive, validating each member's name and type,
// passing its bytes to the writer returned by open, and checking everything
// against the manifest at the end.
func readArchive(r io.Reader, lim Limits, open func(name string) (io.WriteCloser, error)) (Manifest, error) {
	var m Manifest
	gz, err := gzip.NewReader(r)
	if err != nil {
		return m, fmt.Errorf("backup: not a gzip stream: %w", err)
	}
	tr := tar.NewReader(gz)

	seen := map[string]File{}
	var total int64
	var manifestRaw []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("backup: read archive: %w", err)
		}
		if manifestRaw != nil {
			return m, fmt.Errorf("backup: %q follows the manifest", hdr.Name)
		}
		if err := checkMember(hdr); err != nil {
			return m, err
		}
		if _, dup := seen[hdr.Name]; dup {
			return m, fmt.Errorf("backup: duplicate member %q", hdr.Name)
		}
		if hdr.Size > lim.MaxFile {
			return m, fmt.Errorf("backup: %q is %d bytes, over the %d byte limit", hdr.Name, hdr.Size, lim.MaxFile)
		}
		total += hdr.Size
		if total > lim.MaxTotal {
			return m, fmt.Errorf("backup: archive exceeds the %d byte total limit", lim.MaxTotal)
		}

		if hdr.Name == ManifestName {
			if hdr.Size > maxManifest {
				return m, fmt.Errorf("backup: manifest too large")
			}
			manifestRaw, err = io.ReadAll(tr)
			if err != nil {
				return m, fmt.Errorf("backup: read manifest: %w", err)
			}
			if manifestRaw == nil {
				manifestRaw = []byte{} // non-nil marks "manifest seen"
			}
			continue
		}

		w, err := open(hdr.Name)
		if err != nil {
			return m, err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(w, h), tr)
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return m, fmt.Errorf("backup: %s: %w", hdr.Name, err)
		}
		seen[hdr.Name] = File{Path: hdr.Name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	// tar stops at its end-of-archive marker; draining gzip to EOF makes it
	// verify its own CRC and length trailer, which catches truncation.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return m, fmt.Errorf("backup: read archive: %w", err)
	}
	if manifestRaw == nil {
		return m, errors.New("backup: no manifest.json (truncated or not a backup)")
	}
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		return m, fmt.Errorf("backup: parse manifest: %w", err)
	}
	if m.Format != FormatVersion {
		return m, fmt.Errorf("backup: unsupported format version %d (this binary reads %d)", m.Format, FormatVersion)
	}
	return m, checkAgainst(m, seen)
}

// checkAgainst compares what was read with what the manifest promised.
func checkAgainst(m Manifest, seen map[string]File) error {
	listed := map[string]bool{}
	var problems []string
	for _, f := range m.Files {
		listed[f.Path] = true
		got, ok := seen[f.Path]
		switch {
		case !ok:
			problems = append(problems, "missing "+f.Path)
		case got.Size != f.Size:
			problems = append(problems, fmt.Sprintf("%s: size %d, manifest says %d", f.Path, got.Size, f.Size))
		case got.SHA256 != f.SHA256:
			problems = append(problems, f.Path+": checksum mismatch")
		}
	}
	for name := range seen {
		if !listed[name] {
			problems = append(problems, "unexpected "+name)
		}
	}
	if !listed[DBName] {
		problems = append(problems, "manifest lists no "+DBName)
	}
	if len(problems) > 0 {
		return fmt.Errorf("backup: verification failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

// checkMember rejects anything but a regular file at a safe, canonical,
// relative path inside the expected layout. Write never emits directories,
// links or devices, so any of those means the archive isn't ours.
func checkMember(hdr *tar.Header) error {
	name := hdr.Name
	if hdr.Typeflag != tar.TypeReg {
		return fmt.Errorf("backup: %q is not a regular file (type %q)", name, hdr.Typeflag)
	}
	// filepath.IsLocal rejects absolute paths, "..", empty names and (on
	// Windows) reserved names; requiring the cleaned form rejects "a/./b",
	// "a//b" and trailing slashes, so one file has exactly one spelling.
	if name == "" || strings.Contains(name, `\`) || path.Clean(name) != name ||
		!filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("backup: unsafe path %q", name)
	}
	if name != DBName && name != ManifestName && !strings.HasPrefix(name, ImagesDir+"/") {
		return fmt.Errorf("backup: unexpected path %q", name)
	}
	if hdr.Size < 0 {
		return fmt.Errorf("backup: %q has a negative size", name)
	}
	return nil
}
