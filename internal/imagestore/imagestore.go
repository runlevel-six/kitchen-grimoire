// SPDX-License-Identifier: Apache-2.0

// Package imagestore stores recipe images on disk, content-addressed by the
// SHA-256 of the stored bytes (DESIGN.md §7.2, §8.5, §15.2).
//
// Every image that comes in (upload or fetch) is content-sniffed, decoded,
// optionally rotated per its EXIF orientation, shrunk to a 2048 px long edge,
// and re-encoded as JPEG (or PNG when it has transparency). Re-encoding means
// we never serve attacker-controlled bytes and it strips EXIF/GPS metadata.
// A 480 px JPEG thumbnail is stored next to each image.
//
// On-disk layout, where aa and bb are the first two pairs of hex digits of
// the hash:
//
//	<root>/<aa>/<bb>/<sha256>.<jpg|png>
//	<root>/<aa>/<bb>/<sha256>-thumb.jpg
package imagestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	// Imported for their side effect: each registers a decoder with the
	// image package so image.DecodeConfig / image.Decode recognise the
	// format by its magic bytes.
	_ "image/gif"

	_ "golang.org/x/image/webp"
)

// Limits and encoder settings.
const (
	// MaxEdge is the longest side, in pixels, of a stored full-size image.
	MaxEdge = 2048
	// ThumbEdge is the longest side, in pixels, of a thumbnail.
	ThumbEdge = 480
	// JPEGQuality is used for both full-size images and thumbnails.
	JPEGQuality = 85

	// Decompression-bomb guards, checked from the image header before any
	// pixel data is decoded. A tiny compressed file can claim enormous
	// dimensions and make the decoder allocate gigabytes; these caps bound
	// a decode to roughly 50M pixels x 4 bytes = 200 MB in the worst case.
	// 50 MP comfortably covers current phone cameras (typically 12-48 MP).
	defaultMaxPixels = 50_000_000
	defaultMaxSide   = 20_000

	dirPerm  = 0o750
	filePerm = 0o640
)

var (
	// ErrTooLarge means the input stream had more than maxBytes bytes.
	// It is only about byte size; images whose dimensions are too large are
	// reported as ErrUnsupported.
	ErrTooLarge = errors.New("imagestore: image exceeds size limit")

	// ErrUnsupported means the input is not an image we accept: an unknown
	// or disallowed format (AVIF, HEIC, SVG, random bytes...), a corrupt
	// file, or dimensions beyond the decompression-bomb limits. Callers
	// should keep the source URL and show a placeholder (§8.5).
	// Returned errors wrap it, so test with errors.Is.
	ErrUnsupported = errors.New("imagestore: unsupported image")

	// ErrInvalidName is returned by Open, OpenThumb and Delete when the
	// sha/ext pair is not a well-formed name this store could have produced.
	// This keeps user-supplied names from escaping the root directory.
	ErrInvalidName = errors.New("imagestore: invalid image name")
)

// acceptedFormats maps the format names reported by image.DecodeConfig to
// whether we accept them. We use an allow-list because any package in the
// binary can register extra decoders with the image package.
var acceptedFormats = map[string]bool{
	"jpeg": true,
	"png":  true,
	"gif":  true,
	"webp": true,
}

// Store is a directory of content-addressed images. It is safe for
// concurrent use: every write goes through a temp file and an atomic rename,
// and two Puts of the same image produce identical bytes.
type Store struct {
	root string

	// Bomb limits; set by New. Unexported so tests can lower them.
	maxPixels int
	maxSide   int
}

// Stored describes an image written by Put.
type Stored struct {
	SHA256 string // hex SHA-256 of the stored (re-encoded) full-size bytes
	MIME   string // "image/jpeg" or "image/png"
	Ext    string // "jpg" or "png"
	Width  int    // of the stored full-size image
	Height int
}

// New returns a Store rooted at root (for example /data/images), creating
// the directory if needed.
func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("imagestore: empty root")
	}
	if err := os.MkdirAll(root, dirPerm); err != nil {
		return nil, fmt.Errorf("imagestore: create root: %w", err)
	}
	return &Store{root: root, maxPixels: defaultMaxPixels, maxSide: defaultMaxSide}, nil
}

// Put decodes an uploaded or fetched image, re-encodes it, and stores the
// full-size file plus a thumbnail. maxBytes caps how much is read (callers
// pass 15 MiB for uploads, 10 MiB for fetched images); exceeding it returns
// ErrTooLarge. Unacceptable input returns an error wrapping ErrUnsupported.
//
// Put is idempotent: storing the same image again finds the existing files
// and returns the same Stored without rewriting them.
func (s *Store) Put(ctx context.Context, r io.Reader, maxBytes int64) (Stored, error) {
	return s.put(ctx, r, maxBytes, true)
}

// Process does everything Put does except write files: it validates and
// re-encodes the image and reports what Put would store, including the
// SHA-256 of the re-encoded bytes. Bulk-import dry runs use it to compute
// an accurate report without touching the disk.
func (s *Store) Process(ctx context.Context, r io.Reader, maxBytes int64) (Stored, error) {
	return s.put(ctx, r, maxBytes, false)
}

// put is Put and Process; write says whether to store the files.
func (s *Store) put(ctx context.Context, r io.Reader, maxBytes int64, write bool) (Stored, error) {
	if maxBytes <= 0 {
		return Stored{}, fmt.Errorf("imagestore: maxBytes must be positive, got %d", maxBytes)
	}

	// Read at most maxBytes+1: if we get the extra byte, the input is too big.
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return Stored{}, fmt.Errorf("imagestore: read: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return Stored{}, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return Stored{}, err
	}

	// Sniff the format and dimensions from the header only. This reads a
	// few bytes, never the pixel data, so it is safe on hostile input.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Stored{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if !acceptedFormats[format] {
		return Stored{}, fmt.Errorf("%w: format %q", ErrUnsupported, format)
	}
	if err := s.checkDimensions(cfg.Width, cfg.Height); err != nil {
		return Stored{}, err
	}

	// Full decode. For GIF, image.Decode returns only the first frame.
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Stored{}, fmt.Errorf("%w: decode: %v", ErrUnsupported, err)
	}
	// Belt and braces: the decoded bounds should match the header, but
	// re-check in case a decoder disagrees with its own DecodeConfig.
	if err := s.checkDimensions(img.Bounds().Dx(), img.Bounds().Dy()); err != nil {
		return Stored{}, err
	}
	if err := ctx.Err(); err != nil {
		return Stored{}, err
	}

	// JPEG has no alpha channel, so it is always opaque.
	keepPNG := format != "jpeg" && !isOpaque(img)

	// Resize first, then rotate: the long-edge limit doesn't care about
	// orientation, and rotating the small image is far cheaper than
	// rotating a full-resolution one.
	full := toRGBA(img, MaxEdge)
	img = nil // let the GC reclaim the full-resolution decode early
	if format == "jpeg" {
		full = applyOrientation(full, jpegOrientation(data))
	}
	data = nil
	if err := ctx.Err(); err != nil {
		return Stored{}, err
	}

	st := Stored{Width: full.Rect.Dx(), Height: full.Rect.Dy()}
	var fullBuf bytes.Buffer
	if keepPNG {
		st.MIME, st.Ext = "image/png", "png"
		err = png.Encode(&fullBuf, full)
	} else {
		st.MIME, st.Ext = "image/jpeg", "jpg"
		err = jpeg.Encode(&fullBuf, full, &jpeg.Options{Quality: JPEGQuality})
	}
	if err != nil {
		return Stored{}, fmt.Errorf("imagestore: encode: %w", err)
	}
	sum := sha256.Sum256(fullBuf.Bytes())
	st.SHA256 = hex.EncodeToString(sum[:])
	if err := ctx.Err(); err != nil {
		return Stored{}, err
	}
	if !write {
		return st, nil
	}

	if err := os.MkdirAll(filepath.Dir(s.Path(st.SHA256, st.Ext)), dirPerm); err != nil {
		return Stored{}, fmt.Errorf("imagestore: mkdir: %w", err)
	}

	// Write the thumbnail first. Walk only reports full-size files, so this
	// order means any image Walk sees also has its thumbnail.
	thumbPath := s.ThumbPath(st.SHA256)
	if !exists(thumbPath) {
		var thumbBuf bytes.Buffer
		if err := jpeg.Encode(&thumbBuf, thumbnail(full, ThumbEdge), &jpeg.Options{Quality: JPEGQuality}); err != nil {
			return Stored{}, fmt.Errorf("imagestore: encode thumbnail: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return Stored{}, err
		}
		if err := writeAtomic(thumbPath, thumbBuf.Bytes()); err != nil {
			return Stored{}, err
		}
	}
	fullPath := s.Path(st.SHA256, st.Ext)
	if !exists(fullPath) {
		if err := writeAtomic(fullPath, fullBuf.Bytes()); err != nil {
			return Stored{}, err
		}
	}
	return st, nil
}

// checkDimensions enforces the decompression-bomb limits.
func (s *Store) checkDimensions(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("%w: empty image %dx%d", ErrUnsupported, w, h)
	}
	// Check each side first so w*h below can't overflow.
	if w > s.maxSide || h > s.maxSide || w*h > s.maxPixels {
		return fmt.Errorf("%w: dimensions %dx%d exceed limits", ErrUnsupported, w, h)
	}
	return nil
}

// Path returns the full-size file path for sha and ext ("jpg" or "png").
// It returns "" if the name is malformed, so a bad name can never point
// outside the store.
func (s *Store) Path(sha, ext string) string {
	if !validSHA(sha) || !validExt(ext) {
		return ""
	}
	return filepath.Join(s.root, sha[0:2], sha[2:4], sha+"."+ext)
}

// ThumbPath returns the thumbnail path for sha, or "" if sha is malformed.
func (s *Store) ThumbPath(sha string) string {
	if !validSHA(sha) {
		return ""
	}
	return filepath.Join(s.root, sha[0:2], sha[2:4], sha+"-thumb.jpg")
}

// Lookup returns the metadata of an image already in the store, reading
// only its header. ok is false if there is no such file (or it can't be
// read). Bulk import uses it to recognize bundle images that are
// byte-identical to files this store already produced, which then need no
// re-encoding.
func (s *Store) Lookup(sha, ext string) (st Stored, ok bool) {
	f, err := s.Open(sha, ext)
	if err != nil {
		return Stored{}, false
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return Stored{}, false
	}
	st = Stored{SHA256: sha, Ext: ext, MIME: "image/jpeg", Width: cfg.Width, Height: cfg.Height}
	if ext == "png" {
		st.MIME = "image/png"
	}
	return st, true
}

// Open opens the full-size image for reading. The caller must Close it.
func (s *Store) Open(sha, ext string) (*os.File, error) {
	p := s.Path(sha, ext)
	if p == "" {
		return nil, ErrInvalidName
	}
	return os.Open(p)
}

// OpenThumb opens the thumbnail for reading. The caller must Close it.
func (s *Store) OpenThumb(sha string) (*os.File, error) {
	p := s.ThumbPath(sha)
	if p == "" {
		return nil, ErrInvalidName
	}
	return os.Open(p)
}

// Delete removes the full-size image and its thumbnail. Files that are
// already gone are not an error. Empty aa/bb directories are left in place;
// they are harmless and likely to be reused.
func (s *Store) Delete(sha, ext string) error {
	full, thumb := s.Path(sha, ext), s.ThumbPath(sha)
	if full == "" || thumb == "" {
		return ErrInvalidName
	}
	// Remove the full-size file first so Walk never reports an image whose
	// thumbnail has already been deleted.
	for _, p := range []string{full, thumb} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("imagestore: delete: %w", err)
		}
	}
	return nil
}

// Walk calls fn for every stored full-size image, in lexical order.
// Thumbnails, temp files and anything else that isn't a valid image name
// in the right <aa>/<bb> directory are skipped. If fn returns an error,
// Walk stops and returns it.
func (s *Store) Walk(fn func(sha, ext string) error) error {
	return filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		dot := strings.LastIndexByte(name, '.')
		if dot < 0 {
			return nil
		}
		sha, ext := name[:dot], name[dot+1:]
		// Only accept files exactly where Path would have put them.
		if p := s.Path(sha, ext); p == "" || p != path {
			return nil
		}
		return fn(sha, ext)
	})
}

// validSHA reports whether s is 64 lowercase hex digits, the form produced
// by hex.EncodeToString on a SHA-256 sum.
func validSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validExt(ext string) bool { return ext == "jpg" || ext == "png" }

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeAtomic writes data to path so that readers only ever see either no
// file or the complete file: write a temp file in the same directory (rename
// is only atomic within one filesystem), fsync it so the bytes are on disk,
// then rename it over the final name.
func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	// The leading dot and the random suffix keep temp files from matching
	// a real image name, so Walk ignores them.
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("imagestore: create temp: %w", err)
	}
	tmp := f.Name()
	// On any failure below, remove the temp file. The named return value
	// lets this deferred func see the error being returned.
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if err = f.Chmod(filePerm); err != nil { // CreateTemp uses 0o600
		return fmt.Errorf("imagestore: chmod temp: %w", err)
	}
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("imagestore: write temp: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("imagestore: sync temp: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("imagestore: close temp: %w", err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("imagestore: rename: %w", err)
	}
	// Best effort: fsync the directory so the rename itself survives a
	// crash. Some filesystems don't support this, so errors are ignored.
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
