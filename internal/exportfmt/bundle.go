// SPDX-License-Identifier: Apache-2.0

package exportfmt

import (
	"archive/zip"
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// RecipesFile is the name of the recipe list inside a bundle.
const RecipesFile = "recipes.jsonl"

// zipTime is the modification time written on every bundle entry. A fixed
// time makes two exports of the same library byte-identical.
var zipTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// ImageOpener opens a stored image for the bundle writer.
type ImageOpener func(sha, ext string) (io.ReadCloser, error)

// SortForExport orders recipes the way bundles list them: by export key
// (see ExportKey), then title, then ID. Keys are unique within a library,
// so the order is the same in every instance that holds the same recipes.
func SortForExport(recipes []*recipe.Recipe) {
	slices.SortStableFunc(recipes, func(a, b *recipe.Recipe) int {
		return cmp.Or(
			strings.Compare(ExportKey(a), ExportKey(b)),
			strings.Compare(a.Title, b.Title),
			strings.Compare(a.ID, b.ID),
		)
	})
}

// MarshalLine renders one recipe as its recipes.jsonl line (without the
// newline). HTML characters are not escaped, so the file stays readable.
func MarshalLine(r *recipe.Recipe) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ToJSONLD(r)); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// WriteBundle writes a bundle zip of recipes and the images they use to w.
// The output is deterministic: recipes in SortForExport order, images by
// path, each image stored once, and fixed entry timestamps. recipes is
// not modified (a sorted copy is used).
func WriteBundle(w io.Writer, recipes []*recipe.Recipe, open ImageOpener) error {
	sorted := slices.Clone(recipes)
	SortForExport(sorted)

	zw := zip.NewWriter(w)
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: RecipesFile, Method: zip.Deflate, Modified: zipTime})
	if err != nil {
		return err
	}
	images := map[string]recipe.Image{}
	for _, r := range sorted {
		line, err := MarshalLine(r)
		if err != nil {
			return fmt.Errorf("encode %q: %w", r.Title, err)
		}
		if _, err := fw.Write(append(line, '\n')); err != nil {
			return err
		}
		for _, im := range r.Images {
			images[ImagePath(im)] = im
		}
	}

	paths := make([]string, 0, len(images))
	for p := range images {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		im := images[p]
		// JPEG and PNG are already compressed; Store avoids wasted work.
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: p, Method: zip.Store, Modified: zipTime})
		if err != nil {
			return err
		}
		rc, err := open(im.SHA256, imageExt(im.MIME))
		if err != nil {
			return fmt.Errorf("open image %s: %w", p, err)
		}
		_, err = io.Copy(fw, rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("copy image %s: %w", p, err)
		}
	}
	return zw.Close()
}

// Limits bound what ReadBundle accepts. Zip headers declare each entry's
// uncompressed size; ReadBundle checks the declarations up front and then
// never reads more than declared, so a "zip bomb" (a tiny file that
// inflates to gigabytes) is rejected before any inflating.
type Limits struct {
	MaxEntries int   // files in the zip
	MaxRecipes int   // lines in recipes.jsonl
	MaxJSONL   int64 // uncompressed size of recipes.jsonl
	MaxLine    int   // one recipe line
	MaxImage   int64 // one image file
	MaxTotal   int64 // all files together, uncompressed
}

// DefaultLimits are generous for a household library (the legacy
// collection is hundreds of recipes) and small enough to keep a hostile
// upload from exhausting memory or disk.
func DefaultLimits() Limits {
	return Limits{
		MaxEntries: 50_000,
		MaxRecipes: 20_000,
		MaxJSONL:   256 << 20,
		MaxLine:    8 << 20,
		MaxImage:   15 << 20, // the upload limit (§8.5)
		MaxTotal:   4 << 30,
	}
}

// Item is one line of recipes.jsonl. Index is the 0-based line number.
// Err is set (and Object nil) when the line is not a JSON object.
type Item struct {
	Index  int
	Object Object
	Err    error
}

// Bundle is a parsed bundle: its recipe objects and an index of its images.
type Bundle struct {
	Items  []Item
	images map[string]*zip.File // by path, images/<sha>.<ext>
}

// ErrBundle wraps every error ReadBundle returns for a bad bundle (as
// opposed to an I/O failure), so callers can answer 400.
var ErrBundle = errors.New("invalid bundle")

func bad(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBundle, fmt.Sprintf(format, args...))
}

// ReadBundle parses a bundle zip of the given size. The zip may contain
// only recipes.jsonl, an images/ directory entry, and image files named
// images/<sha256>.<jpg|png>; anything else (absolute paths, "..",
// backslashes, other names, symlinks, duplicates) rejects the bundle.
// Lines that are not JSON objects are kept as Items with Err set, so the
// import report can list them and carry on.
func ReadBundle(r io.ReaderAt, size int64, lim Limits) (*Bundle, error) {
	zr, err := zip.NewReader(r, size)
	if errors.Is(err, zip.ErrInsecurePath) {
		return nil, bad("unsafe file name in zip")
	}
	if err != nil {
		return nil, bad("not a zip file: %v", err)
	}
	if len(zr.File) > lim.MaxEntries {
		return nil, bad("too many files (%d, max %d)", len(zr.File), lim.MaxEntries)
	}
	b := &Bundle{images: map[string]*zip.File{}}
	var recipesFile *zip.File
	var total uint64
	for _, f := range zr.File {
		name := f.Name
		if err := checkName(name); err != nil {
			return nil, err
		}
		if name == "images/" && f.Mode().IsDir() {
			continue // some zip tools add directory entries
		}
		if !f.Mode().IsRegular() {
			return nil, bad("%q is not a regular file", clip(name, 80))
		}
		total += f.UncompressedSize64
		if total > uint64(lim.MaxTotal) {
			return nil, bad("contents too large (max %d bytes uncompressed)", lim.MaxTotal)
		}
		switch {
		case name == RecipesFile:
			if recipesFile != nil {
				return nil, bad("duplicate %s", RecipesFile)
			}
			if f.UncompressedSize64 > uint64(lim.MaxJSONL) {
				return nil, bad("%s too large (max %d bytes)", RecipesFile, lim.MaxJSONL)
			}
			recipesFile = f
		default:
			if _, _, ok := ParseImagePath(name); !ok {
				return nil, bad("unexpected file %q (allowed: %s and images/<sha256>.<jpg|png>)", clip(name, 80), RecipesFile)
			}
			if _, dup := b.images[name]; dup {
				return nil, bad("duplicate file %q", name)
			}
			if f.UncompressedSize64 > uint64(lim.MaxImage) {
				return nil, bad("image %q too large (max %d bytes)", name, lim.MaxImage)
			}
			b.images[name] = f
		}
	}
	if recipesFile == nil {
		return nil, bad("missing %s", RecipesFile)
	}

	rc, err := recipesFile.Open()
	if err != nil {
		return nil, bad("open %s: %v", RecipesFile, err)
	}
	defer rc.Close()
	// archive/zip itself fails a read that goes past the declared size, and
	// checks the CRC at the end, so the size checks above are enforced.
	sc := bufio.NewScanner(rc)
	// Scanner's limit is the larger of the cap and the initial buffer's
	// capacity, so the buffer must not start bigger than the cap.
	sc.Buffer(make([]byte, 0, min(64<<10, lim.MaxLine)), lim.MaxLine)
	for i := 0; sc.Scan(); i++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if len(b.Items) >= lim.MaxRecipes {
			return nil, bad("too many recipes (max %d)", lim.MaxRecipes)
		}
		obj, err := decodeLine(line)
		b.Items = append(b.Items, Item{Index: i, Object: obj, Err: err})
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, bad("a line in %s is longer than %d bytes", RecipesFile, lim.MaxLine)
		}
		return nil, bad("read %s: %v", RecipesFile, err)
	}
	return b, nil
}

// checkName rejects entry names that could escape a directory if anyone
// ever extracted the bundle, before matching the allowed patterns.
func checkName(name string) error {
	switch {
	case name == "":
		return bad("empty file name")
	case strings.ContainsRune(name, '\\'):
		return bad("backslash in file name %q", clip(name, 80))
	case strings.HasPrefix(name, "/") || strings.Contains(name, ":"):
		return bad("absolute file name %q", clip(name, 80))
	case !fs.ValidPath(strings.TrimSuffix(name, "/")):
		// fs.ValidPath rejects "..", ".", empty elements, and leading "/".
		return bad("unsafe file name %q", clip(name, 80))
	}
	return nil
}

// decodeLine parses one JSON object, keeping numbers as json.Number.
func decodeLine(line []byte) (Object, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("not a JSON object: %v", err)
	}
	if obj == nil {
		return nil, errors.New("not a JSON object: null")
	}
	if dec.More() {
		return nil, errors.New("more than one JSON value on the line")
	}
	return Object(obj), nil
}

// HasImage reports whether the bundle contains the image file.
func (b *Bundle) HasImage(sha, ext string) bool {
	_, ok := b.images["images/"+sha+"."+ext]
	return ok
}

// ImageCount is the number of image files in the bundle.
func (b *Bundle) ImageCount() int { return len(b.images) }

// OpenImage opens an image file from the bundle. ReadBundle capped its
// declared size at Limits.MaxImage, and archive/zip fails any read past
// the declared size, so the reader yields at most that many bytes.
func (b *Bundle) OpenImage(sha, ext string) (io.ReadCloser, error) {
	f, ok := b.images["images/"+sha+"."+ext]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return f.Open()
}
