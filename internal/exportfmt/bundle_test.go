// SPDX-License-Identifier: Apache-2.0

package exportfmt

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// fakeImages serves image bytes derived from the hash, so tests need no
// real image store.
func fakeImages(sha, ext string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("bytes of " + sha + "." + ext)), nil
}

func writeBundle(t testing.TB, recipes ...*recipe.Recipe) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteBundle(&buf, recipes, fakeImages); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readBundle(data []byte, lim Limits) (*Bundle, error) {
	return ReadBundle(bytes.NewReader(data), int64(len(data)), lim)
}

func TestWriteReadBundle(t *testing.T) {
	a := fullRecipe()
	b := &recipe.Recipe{ID: "id-b", Title: "Another", ImportKey: "a-first",
		Images: []recipe.Image{{SHA256: sha('a'), MIME: "image/jpeg"}}} // shares an image with a
	c := &recipe.Recipe{ID: "id-c", Title: "Keyless"}

	data := writeBundle(t, a, b, c)
	if again := writeBundle(t, c, b, a); !bytes.Equal(data, again) {
		t.Fatal("bundle bytes depend on input order")
	}

	bun, err := readBundle(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(bun.Items) != 3 || bun.ImageCount() != 2 {
		t.Fatalf("items %d images %d", len(bun.Items), bun.ImageCount())
	}
	// Order: export key, so "a-first" < "legacy:..." < "recipe:id-c".
	var titles []string
	for _, it := range bun.Items {
		if it.Err != nil {
			t.Fatal(it.Err)
		}
		titles = append(titles, it.Object["name"].(string))
	}
	if strings.Join(titles, "|") != "Another|"+a.Title+"|Keyless" {
		t.Errorf("order = %v", titles)
	}
	rc, err := bun.OpenImage(sha('b'), "png")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "bytes of "+sha('b')+".png" {
		t.Errorf("image bytes = %q", got)
	}
	if !bun.HasImage(sha('a'), "jpg") || bun.HasImage(sha('a'), "png") {
		t.Error("HasImage wrong")
	}
	if _, err := bun.OpenImage(sha('c'), "jpg"); err == nil {
		t.Error("opened a missing image")
	}
}

type entry struct {
	name string
	body string
}

func zipOf(t testing.TB, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, e.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReadBundleKeepsBadLines(t *testing.T) {
	data := zipOf(t, entry{RecipesFile, `{"@type":"Recipe","name":"ok"}` + "\n\n" +
		"not json\n" + `[1,2]` + "\n" + `{"a":1} {"b":2}` + "\nnull\n"})
	b, err := readBundle(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Items) != 5 {
		t.Fatalf("items = %d, want 5", len(b.Items))
	}
	if b.Items[0].Err != nil || b.Items[1].Index != 2 {
		t.Errorf("first item %+v, second index %d", b.Items[0], b.Items[1].Index)
	}
	for _, it := range b.Items[1:] {
		if it.Err == nil {
			t.Errorf("line %d: no error", it.Index)
		}
	}
}

func TestReadBundleRejects(t *testing.T) {
	img := "images/" + sha('a') + ".jpg"
	ok := entry{RecipesFile, "{}\n"}
	small := DefaultLimits()
	small.MaxEntries, small.MaxRecipes, small.MaxJSONL, small.MaxLine, small.MaxImage, small.MaxTotal = 3, 2, 1000, 100, 50, 1050

	for name, data := range map[string][]byte{
		"not a zip":         []byte("PK nope"),
		"missing recipes":   zipOf(t, entry{img, "x"}),
		"duplicate recipes": zipOf(t, ok, ok),
		"duplicate image":   zipOf(t, ok, entry{img, "x"}, entry{img, "x"}),
		"traversal":         zipOf(t, ok, entry{"../" + RecipesFile, "x"}),
		"inner traversal":   zipOf(t, ok, entry{"images/../../etc/passwd", "x"}),
		"absolute":          zipOf(t, ok, entry{"/etc/passwd", "x"}),
		"drive letter":      zipOf(t, ok, entry{"C:/x", "x"}),
		"backslash":         zipOf(t, ok, entry{`images\` + sha('a') + ".jpg", "x"}),
		"other file":        zipOf(t, ok, entry{"notes.txt", "x"}),
		"other dir":         zipOf(t, ok, entry{"__MACOSX/", ""}),
		"bad image name":    zipOf(t, ok, entry{"images/photo.jpg", "x"}),
		"too many entries":  zipOf(t, ok, entry{"images/", ""}, entry{img, "x"}, entry{"images/" + sha('b') + ".jpg", "x"}),
		"too many recipes":  zipOf(t, entry{RecipesFile, "{}\n{}\n{}\n"}),
		"long line":         zipOf(t, entry{RecipesFile, `{"name":"` + strings.Repeat("x", 200) + `"}`}),
		// A zip bomb: tiny compressed, large declared uncompressed size.
		"jsonl too large": zipOf(t, entry{RecipesFile, strings.Repeat("\n", 5000)}),
		"image too large": zipOf(t, ok, entry{img, strings.Repeat("x", 51)}),
		"total too large": zipOf(t, entry{RecipesFile, strings.Repeat("\n", 999)}, entry{img, strings.Repeat("x", 50)},
			entry{"images/" + sha('b') + ".jpg", strings.Repeat("x", 50)}),
	} {
		_, err := readBundle(data, small)
		if !errors.Is(err, ErrBundle) {
			t.Errorf("%s: err = %v, want ErrBundle", name, err)
		}
	}

	// The directory entry for images/ itself is fine.
	if _, err := readBundle(zipOf(t, ok, entry{"images/", ""}, entry{img, "x"}), small); err != nil {
		t.Errorf("images/ dir entry: %v", err)
	}
}

func TestReadBundleSizeLies(t *testing.T) {
	// Rewrite the declared sizes in the central directory to claim a
	// small file, then check that reading past the claim fails instead of
	// inflating everything.
	data := zipOf(t, entry{RecipesFile, `{"name":"` + strings.Repeat("x", 10000) + `"}` + "\n"})
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	f := zr.File[0]
	f.UncompressedSize64 = 10
	rc, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if n, err := io.Copy(io.Discard, rc); err == nil || n > 10 {
		t.Errorf("read %d bytes past a 10-byte declaration, err = %v", n, err)
	}
}
