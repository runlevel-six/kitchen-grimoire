// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// fixture is a populated data directory: a migrated database with a few
// invented recipes (one in the trash) and two images.
type fixture struct {
	dir    string
	db     *store.Store
	files  *imagestore.Store
	recipe *recipe.Recipe // one with an image, for a full-content comparison
	images []imagestore.Stored
}

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.OpenDataDir(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	svc := recipe.NewService(st)
	files, err := imagestore.New(filepath.Join(dir, "images"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: dir, db: st, files: files}

	titles := []string{"Example Lemon Bars", "Invented Bean Soup", "Sample Oat Cookies"}
	var all []*recipe.Recipe
	for _, title := range titles {
		r := &recipe.Recipe{
			Title:     title,
			YieldText: "4 servings",
			Servings:  4,
			IngredientGroups: []recipe.IngredientGroup{
				{Ingredients: []recipe.Ingredient{{Raw: "2 cups flour"}, {Raw: "1 pinch salt"}}},
			},
			StepGroups:    []recipe.StepGroup{{Steps: []string{"Mix.", "Bake 20 minutes."}}},
			Categories:    [][]string{{"Desserts", "Bars"}},
			Tags:          []string{"party"},
			ContributedBy: "Example Contributor",
		}
		if err := svc.Create(ctx, r, ""); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	// The trash is part of the backup too.
	if err := svc.Delete(ctx, all[2].ID); err != nil {
		t.Fatal(err)
	}

	for _, c := range []color.Color{color.NRGBA{200, 30, 30, 255}, color.NRGBA{30, 30, 200, 128}} {
		s, err := files.Put(ctx, bytes.NewReader(pngBytes(t, 64, 48, c)), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddImage(ctx, all[0].ID, recipe.Image{SHA256: s.SHA256, MIME: s.MIME, Width: s.Width, Height: s.Height}); err != nil {
			t.Fatal(err)
		}
		f.images = append(f.images, s)
	}
	f.recipe, err = st.GetRecipe(ctx, all[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) backup(t *testing.T) ([]byte, Manifest) {
	t.Helper()
	var buf bytes.Buffer
	m, err := Write(context.Background(), f.db.DB(), f.files, &buf, Meta{AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), m
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	archive, m := f.backup(t)

	if m.Format != 1 || m.AppVersion != "test" || m.SchemaVersion < 1 ||
		m.Counts.Recipes != 3 || m.Counts.Images != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	// db + 2 images + 2 thumbnails
	if len(m.Files) != 5 || m.Files[0].Path != DBName {
		t.Fatalf("manifest files = %+v", m.Files)
	}
	if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil || !strings.HasSuffix(m.CreatedAt, "Z") {
		t.Errorf("created_at %q is not UTC RFC 3339", m.CreatedAt)
	}
	names := archiveNames(t, archive)
	if names[len(names)-1] != ManifestName {
		t.Errorf("manifest is not the last member: %v", names)
	}

	vm, err := Verify(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !reflect.DeepEqual(vm, m) {
		t.Errorf("Verify manifest differs from Write's:\n%+v\n%+v", vm, m)
	}

	// Restore into a directory that doesn't exist yet.
	dst := filepath.Join(t.TempDir(), "restored")
	rm, err := Restore(ctx, bytes.NewReader(archive), dst, false)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if rm.Counts != m.Counts {
		t.Errorf("restored counts %+v, want %+v", rm.Counts, m.Counts)
	}

	db, err := store.OpenDataDir(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM recipes").Scan(&n); err != nil || n != 3 {
		t.Fatalf("restored recipes = %d (%v), want 3", n, err)
	}
	got, err := store.New(db).GetRecipe(ctx, f.recipe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, f.recipe) {
		t.Errorf("restored recipe differs:\n got %+v\nwant %+v", got, f.recipe)
	}
	restoredFiles, err := imagestore.New(filepath.Join(dst, "images"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range f.images {
		for _, pair := range [][2]string{
			{f.files.Path(s.SHA256, s.Ext), restoredFiles.Path(s.SHA256, s.Ext)},
			{f.files.ThumbPath(s.SHA256), restoredFiles.ThumbPath(s.SHA256)},
		} {
			want, err1 := os.ReadFile(pair[0])
			have, err2 := os.ReadFile(pair[1])
			if err1 != nil || err2 != nil || !bytes.Equal(want, have) {
				t.Errorf("%s: restored bytes differ (%v, %v)", pair[1], err1, err2)
			}
		}
	}
	// No staging leftovers.
	entries, _ := os.ReadDir(dst)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore-") {
			t.Errorf("staging dir %s left behind", e.Name())
		}
	}
	db.Close() // the server would be stopped before a forced restore

	// Second restore: refused without force, and nothing changes.
	marker := filepath.Join(dst, "keep-me.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, dst)
	if _, err := Restore(ctx, bytes.NewReader(archive), dst, false); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("restore without force: err = %v, want ErrNotEmpty", err)
	}
	if after := treeSnapshot(t, dst); !reflect.DeepEqual(before, after) {
		t.Error("refused restore changed the data dir")
	}
	// A stale WAL must not survive a forced restore.
	if err := os.WriteFile(filepath.Join(dst, DBName+"-wal"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, bytes.NewReader(archive), dst, true); err != nil {
		t.Fatalf("restore with force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, DBName+"-wal")); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale -wal file survived a forced restore")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("forced restore removed an unrelated file")
	}
	db2, err := store.OpenDataDir(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.QueryRowContext(ctx, "SELECT count(*) FROM recipes").Scan(&n); err != nil || n != 3 {
		t.Fatalf("after forced restore: recipes = %d (%v)", n, err)
	}
}

func TestRestoreFailureLeavesDataDirAlone(t *testing.T) {
	f := newFixture(t)
	archive, _ := f.backup(t)
	entries := readEntries(t, archive)
	entries[1].body = flip(entries[1].body) // corrupt an image

	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, DBName), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, dst)
	if _, err := Restore(context.Background(), bytes.NewReader(pack(t, entries)), dst, true); err == nil {
		t.Fatal("restore of a corrupt archive succeeded")
	}
	if after := treeSnapshot(t, dst); !reflect.DeepEqual(before, after) {
		t.Errorf("failed restore changed the data dir:\n%v\n%v", before, after)
	}

	// A data dir that didn't exist is removed again.
	missing := filepath.Join(t.TempDir(), "new")
	if _, err := Restore(context.Background(), bytes.NewReader(pack(t, entries)), missing, false); err == nil {
		t.Fatal("restore of a corrupt archive succeeded")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Error("failed restore left a newly created data dir behind")
	}
}

func TestRestoreIgnoresLostAndFound(t *testing.T) {
	f := newFixture(t)
	archive, _ := f.backup(t)
	dst := t.TempDir()
	if err := os.Mkdir(filepath.Join(dst, "lost+found"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), bytes.NewReader(archive), dst, false); err != nil {
		t.Fatalf("restore into a fresh volume: %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	f := newFixture(t)
	archive, _ := f.backup(t)

	tests := []struct {
		name   string
		mutate func([]entry) []entry
		want   string
	}{
		{"flipped byte", func(es []entry) []entry {
			es[0].body = flip(es[0].body)
			return es
		}, "checksum mismatch"},
		{"missing file", func(es []entry) []entry {
			return slices.Delete(es, 1, 2)
		}, "missing images/"},
		{"extra file", func(es []entry) []entry {
			extra := entry{hdr: tar.Header{Name: "images/zz/zz/extra.jpg"}, body: []byte("surprise")}
			return slices.Insert(es, 1, extra)
		}, "unexpected images/zz/zz/extra.jpg"},
		{"no manifest", func(es []entry) []entry {
			return es[:len(es)-1]
		}, "no manifest.json"},
		{"member after manifest", func(es []entry) []entry {
			return append(es, entry{hdr: tar.Header{Name: "images/aa/bb/late.jpg"}, body: []byte("x")})
		}, "follows the manifest"},
		{"duplicate", func(es []entry) []entry {
			return slices.Insert(es, 1, es[0])
		}, "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := pack(t, tt.mutate(readEntries(t, archive)))
			_, err := Verify(bytes.NewReader(bad))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Verify err = %v, want it to mention %q", err, tt.want)
			}
		})
	}

	t.Run("truncated", func(t *testing.T) {
		if _, err := Verify(bytes.NewReader(archive[:len(archive)-100])); err == nil {
			t.Error("Verify accepted a truncated archive")
		}
	})
}

func TestRestoreRejectsMaliciousArchives(t *testing.T) {
	tests := []struct {
		name string
		hdr  tar.Header
		want string
	}{
		{"parent traversal", tar.Header{Name: "../evil"}, "unsafe path"},
		{"nested traversal", tar.Header{Name: "images/../../evil"}, "unsafe path"},
		{"absolute", tar.Header{Name: "/etc/evil"}, "unsafe path"},
		{"dot segment", tar.Header{Name: "images/./a.jpg"}, "unsafe path"},
		{"backslash", tar.Header{Name: `images\..\..\evil`}, "unsafe path"},
		{"outside layout", tar.Header{Name: "evil.sh"}, "unexpected path"},
		{"symlink", tar.Header{Name: "images/aa/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, "not a regular file"},
		{"hardlink", tar.Header{Name: "images/aa/link", Typeflag: tar.TypeLink, Linkname: "grimoire.db"}, "not a regular file"},
		{"device", tar.Header{Name: "images/aa/dev", Typeflag: tar.TypeChar}, "not a regular file"},
		{"directory", tar.Header{Name: "images/aa/", Typeflag: tar.TypeDir}, "not a regular file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			dst := filepath.Join(parent, "data")
			archive := pack(t, []entry{{hdr: tt.hdr, body: []byte("x")}})
			_, err := Restore(context.Background(), bytes.NewReader(archive), dst, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
			// Nothing was written anywhere under parent.
			if ents, _ := os.ReadDir(parent); len(ents) != 0 {
				t.Errorf("restore left files behind: %v", ents)
			}
		})
	}
}

func TestRestoreSizeLimits(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 100)
	entries := []entry{
		{hdr: tar.Header{Name: "images/aa/bb/one.jpg"}, body: big},
		{hdr: tar.Header{Name: "images/aa/bb/two.jpg"}, body: big},
	}
	for _, tc := range []struct {
		lim  Limits
		want string
	}{
		{Limits{MaxFile: 99, MaxTotal: 1000}, "over the 99 byte limit"},
		{Limits{MaxFile: 100, MaxTotal: 150}, "total limit"},
	} {
		dst := filepath.Join(t.TempDir(), "data")
		_, err := restore(context.Background(), bytes.NewReader(pack(t, entries)), dst, false, tc.lim)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("limits %+v: err = %v, want %q", tc.lim, err, tc.want)
		}
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	var names []string
	for i := range 5 {
		n := FileName(base.Add(time.Duration(i) * 24 * time.Hour))
		names = append(names, n)
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other := []string{"notes.txt", ".partial-123"}
	for _, n := range other {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Errorf("removed %v, want 3 files", removed)
	}
	left, _ := os.ReadDir(dir)
	var got []string
	for _, e := range left {
		got = append(got, e.Name())
	}
	want := append([]string{names[3], names[4]}, other...)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("left %v, want %v", got, want)
	}
	if _, err := Prune(dir, 0); err == nil {
		t.Error("Prune(keep=0) should refuse")
	}
	if names[0] != "grimoire-20261101T030000Z.tar.gz" {
		t.Errorf("FileName = %q", names[0])
	}
}

// ---- archive helpers ----

type entry struct {
	hdr  tar.Header
	body []byte
}

// pack builds a .tar.gz by hand. Regular-file headers get their size and
// type filled in; other types are written as given (with no body).
func pack(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readEntries(t *testing.T, archive []byte) []entry {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []entry
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, entry{hdr: *h, body: body})
	}
}

func archiveNames(t *testing.T, archive []byte) []string {
	var names []string
	for _, e := range readEntries(t, archive) {
		names = append(names, e.hdr.Name)
	}
	return names
}

func flip(b []byte) []byte {
	c := slices.Clone(b)
	c[len(c)/2] ^= 0x01
	return c
}

// treeSnapshot maps every path under dir to its contents ("<dir>" for
// directories), to check that a failed restore changed nothing.
func treeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			out[rel] = "<dir>"
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
