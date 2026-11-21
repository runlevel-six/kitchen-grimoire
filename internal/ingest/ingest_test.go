// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/exportfmt"
	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// instance is one empty library: database, image store, service, importer.
type instance struct {
	st    *store.Store
	svc   *recipe.Service
	files *imagestore.Store
	imp   *Importer
}

func newInstance(t *testing.T) *instance {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenDataDir(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	files, err := imagestore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	svc := recipe.NewService(st)
	return &instance{st: st, svc: svc, files: files, imp: New(svc, st, files)}
}

func (in *instance) export(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := in.imp.Export(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (in *instance) importBytes(t *testing.T, data []byte, dry bool) Report {
	t.Helper()
	b, err := exportfmt.ReadBundle(bytes.NewReader(data), int64(len(data)), exportfmt.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := in.imp.ImportBundle(context.Background(), b, Options{DryRun: dry})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func (in *instance) count(t *testing.T) int {
	t.Helper()
	ids, err := in.st.LiveRecipeIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(ids)
}

func (in *instance) imageFiles(t *testing.T) int {
	t.Helper()
	n := 0
	if err := in.files.Walk(func(string, string) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	return n
}

func (in *instance) addPhoto(t *testing.T, r *recipe.Recipe, data []byte) recipe.Image {
	t.Helper()
	ctx := context.Background()
	stored, err := in.files.Put(ctx, bytes.NewReader(data), MaxImageBytes)
	if err != nil {
		t.Fatal(err)
	}
	im, err := in.st.AddImage(ctx, r.ID, recipe.Image{SHA256: stored.SHA256, MIME: stored.MIME, Width: stored.Width, Height: stored.Height})
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func jpegBytes(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := range 48 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 5), 90, 255})
		}
	}
	img.Set(0, 0, c)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func transparentPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for x := range 10 {
		img.Set(x, 3, color.NRGBA{200, 10, 10, 120})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seed fills an instance with invented recipes covering every exported
// field: groups, category paths, tags, provenance, original text, review
// reasons, and images (shared, multiple, non-first primary).
func seed(t *testing.T, in *instance) map[string]*recipe.Recipe {
	t.Helper()
	ctx := context.Background()
	out := map[string]*recipe.Recipe{}

	legacy := &recipe.Recipe{
		Title:         "Example Cheese Ball",
		YieldText:     "1 ball",
		Servings:      12,
		PrepMin:       15,
		TotalMin:      135,
		SourceKind:    recipe.SourceLegacy,
		ContributedBy: "Example Contributor",
		Collection:    "Example Family Cookbook",
		ImportKey:     "legacy:example-family-cookbook:0042:example-cheese-ball",
		OriginalText:  "EXAMPLE CHEESE BALL\tAppetizers\tExample Contributor\n1 8-oz pkg cream cheese\nBeat. Chill.",
		NeedsReview:   true,
		ReviewReasons: []string{"unclassified_token:Zzz", "multi_column_ingredients"},
		Categories:    [][]string{{"Appetizers", "Spreads"}, {"Party Food"}},
		Tags:          []string{"make-ahead", "cheese, sharp"},
		IngredientGroups: []recipe.IngredientGroup{
			{Ingredients: []recipe.Ingredient{{Raw: "1 8-oz pkg cream cheese, softened"}, {Raw: "1½ cups shredded cheddar"}}},
			{Name: "Coating", Ingredients: []recipe.Ingredient{{Raw: "½ cup chopped pecans"}}},
		},
		StepGroups: []recipe.StepGroup{
			{Name: "Ball", Steps: []string{"Beat cream cheese & cheddar.", "Shape into a ball."}},
			{Steps: []string{"Chill 2 hours."}},
		},
		Notes:     "Doubles well.\n\nUse sharp cheddar.",
		CreatedAt: time.Date(1999, 12, 24, 18, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2005, 6, 1, 9, 30, 0, 250_000_000, time.UTC),
	}
	if err := in.svc.CreateImported(ctx, legacy, ""); err != nil {
		t.Fatal(err)
	}
	out["legacy"] = legacy

	manual := &recipe.Recipe{
		Title:            "Example Weeknight Soup",
		Description:      "Fast. <Really> fast.",
		CookMin:          30,
		Author:           "Example Chef",
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "4 cups stock"}, {Raw: "salt to taste"}}}},
		StepGroups:       []recipe.StepGroup{{Steps: []string{"Simmer 20 minutes."}}},
		Tags:             []string{"easy"},
		Categories:       [][]string{{"Soups"}},
	}
	if err := in.svc.Create(ctx, manual, ""); err != nil {
		t.Fatal(err)
	}
	out["manual"] = manual

	fromURL := &recipe.Recipe{
		Title:            "Example Lemon Bars",
		SourceKind:       recipe.SourceURL,
		SourceURL:        "https://example.com/lemon-bars?utm_source=x",
		CanonicalURL:     "https://example.com/lemon-bars",
		SourceSite:       "example.com",
		RawSource:        json.RawMessage(`{"@type":"Recipe","name":"Example Lemon Bars","recipeYield":["24",  "24 bars"]}`),
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "4 eggs"}}}},
		Categories:       [][]string{{"Desserts", "Bars"}, {"Appetizers", "Spreads"}},
	}
	if err := in.svc.Create(ctx, fromURL, ""); err != nil {
		t.Fatal(err)
	}
	out["url"] = fromURL

	// Photos: the soup has two (the second made primary) and shares one
	// with the lemon bars; the lemon bars also have a transparent PNG.
	red, blue := jpegBytes(t, color.RGBA{255, 0, 0, 255}), jpegBytes(t, color.RGBA{0, 0, 255, 255})
	in.addPhoto(t, manual, red)
	second := in.addPhoto(t, manual, blue)
	if err := in.st.SetPrimaryImage(ctx, manual.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	in.addPhoto(t, fromURL, transparentPNG(t))
	in.addPhoto(t, fromURL, red)

	// Trash is not exported.
	gone := &recipe.Recipe{Title: "Example Trashed Recipe"}
	if err := in.svc.Create(ctx, gone, ""); err != nil {
		t.Fatal(err)
	}
	if err := in.svc.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	return out
}

// bundleText reads a bundle back as its recipes.jsonl text and image list.
func bundleText(t *testing.T, data []byte) (string, []string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var images []string
	for _, f := range zr.File {
		if f.Name == exportfmt.RecipesFile {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			text = string(b)
		} else {
			images = append(images, f.Name)
		}
	}
	return text, images
}

var imageRef = regexp.MustCompile(`images/[0-9a-f]{64}\.(jpg|png)`)

// normalizeBundle is the documented round-trip normalization (§16).
// Importing re-encodes every image (§8.5), and lossy re-encoding never
// reproduces the same bytes, so a new instance holds different image
// files with different hashes. The normalization renames each image to
// its order of first appearance in recipes.jsonl ("images/#1.jpg"), and
// compares image files by count and extension. Everything else must match
// byte for byte.
func normalizeBundle(t *testing.T, data []byte) string {
	t.Helper()
	text, images := bundleText(t, data)
	names := map[string]string{}
	text = imageRef.ReplaceAllStringFunc(text, func(ref string) string {
		if _, ok := names[ref]; !ok {
			names[ref] = fmt.Sprintf("images/#%d.%s", len(names)+1, ref[len(ref)-3:])
		}
		return names[ref]
	})
	if len(names) != len(images) {
		t.Errorf("bundle has %d image files but references %d", len(images), len(names))
	}
	for _, im := range images {
		if _, ok := names[im]; !ok {
			t.Errorf("unreferenced image file %s", im)
		}
	}
	return text
}

func wantCounts(t *testing.T, rep Report, want Counts) {
	t.Helper()
	if rep.Counts != want {
		b, _ := json.MarshalIndent(rep, "", "  ")
		t.Fatalf("counts = %+v, want %+v\n%s", rep.Counts, want, b)
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := newInstance(t)
	seeded := seed(t, a)
	exportA := a.export(t)
	if again := a.export(t); !bytes.Equal(exportA, again) {
		t.Fatal("two exports of the same library differ")
	}
	textA, imagesA := bundleText(t, exportA)
	if n := strings.Count(textA, "\n"); n != 3 {
		t.Fatalf("export has %d recipes, want 3 (trash excluded)", n)
	}
	if len(imagesA) != 3 {
		t.Fatalf("export has %d image files, want 3 (shared photo stored once)", len(imagesA))
	}

	// Dry run into an empty instance: full report, nothing written.
	b := newInstance(t)
	rep := b.importBytes(t, exportA, true)
	wantCounts(t, rep, Counts{Created: 3})
	if !rep.DryRun || b.count(t) != 0 || b.imageFiles(t) != 0 {
		t.Fatalf("dry run wrote: %d recipes, %d images", b.count(t), b.imageFiles(t))
	}

	// The real import, then export again.
	rep = b.importBytes(t, exportA, false)
	wantCounts(t, rep, Counts{Created: 3})
	for _, it := range rep.Items {
		if it.Slug == "" || len(it.Warnings) > 0 {
			t.Errorf("item %+v", it)
		}
	}
	exportB := b.export(t)
	if na, nb := normalizeBundle(t, exportA), normalizeBundle(t, exportB); na != nb {
		t.Fatalf("round trip differs after normalization:\nA: %s\nB: %s", na, nb)
	}

	// B's recipes carry the same provenance and dates as A's.
	got, err := b.st.GetRecipeByImportKey(ctx, "recipe:"+seeded["manual"].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || got.ImportRevision != 1 || !got.CreatedAt.Equal(seeded["manual"].CreatedAt) ||
		len(got.Images) != 2 || got.Images[0].IsPrimary != true {
		t.Errorf("imported soup: rev %d/%d created %v images %+v", got.Revision, got.ImportRevision, got.CreatedAt, got.Images)
	}

	// A second generation is stable too (export B → empty C → export C).
	c := newInstance(t)
	wantCounts(t, c.importBytes(t, exportB, false), Counts{Created: 3})
	if nb, nc := normalizeBundle(t, exportB), normalizeBundle(t, c.export(t)); nb != nc {
		t.Fatalf("second generation differs:\nB: %s\nC: %s", nb, nc)
	}

	// Re-importing is a no-op: the original bundle, B's own export (whose
	// images B already holds byte for byte), and A's export into A itself.
	wantCounts(t, b.importBytes(t, exportA, false), Counts{Unchanged: 3})
	wantCounts(t, b.importBytes(t, exportB, false), Counts{Unchanged: 3})
	wantCounts(t, a.importBytes(t, exportA, false), Counts{Unchanged: 3})
	if !bytes.Equal(exportB, b.export(t)) || !bytes.Equal(exportA, a.export(t)) {
		t.Fatal("a no-op re-import changed an export")
	}
	if a.count(t) != 3 || b.count(t) != 3 {
		t.Fatalf("re-import created twins: A has %d, B has %d", a.count(t), b.count(t))
	}
}

func TestReimportConflictsAndUpdates(t *testing.T) {
	ctx := context.Background()
	a := newInstance(t)
	seeded := seed(t, a)
	b := newInstance(t)
	b.importBytes(t, a.export(t), false)

	// A person edits the cheese ball in B.
	cheese, err := b.st.GetRecipeByImportKey(ctx, seeded["legacy"].ImportKey)
	if err != nil {
		t.Fatal(err)
	}
	cheese.Notes = "Edited in the kitchen."
	if err := b.svc.Update(ctx, cheese, ""); err != nil {
		t.Fatal(err)
	}

	// Meanwhile A changes the cheese ball and the soup (text and photos).
	for _, key := range []string{"legacy", "manual"} {
		r, err := a.svc.GetByID(ctx, seeded[key].ID)
		if err != nil {
			t.Fatal(err)
		}
		r.Description = "Revised in A."
		if err := a.svc.Update(ctx, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	a.addPhoto(t, seeded["manual"], transparentPNG(t))
	bundle := a.export(t)

	// Dry run first: the same report, nothing written.
	want := Counts{Updated: 1, SkippedConflict: 1, Unchanged: 1}
	wantCounts(t, b.importBytes(t, bundle, true), want)
	soupKey := "recipe:" + seeded["manual"].ID
	if soup, _ := b.st.GetRecipeByImportKey(ctx, soupKey); soup.Revision != 1 {
		t.Fatalf("dry run updated the soup (revision %d)", soup.Revision)
	}

	rep := b.importBytes(t, bundle, false)
	wantCounts(t, rep, want)
	for _, it := range rep.Items {
		if it.Outcome == SkippedConflict && (it.ImportKey != seeded["legacy"].ImportKey || !strings.Contains(it.Reasons[0], "edited since")) {
			t.Errorf("conflict item %+v", it)
		}
	}
	soup, err := b.st.GetRecipeByImportKey(ctx, soupKey)
	if err != nil {
		t.Fatal(err)
	}
	if soup.Revision != 2 || soup.ImportRevision != 2 || soup.Description != "Revised in A." || len(soup.Images) != 3 {
		t.Errorf("updated soup: rev %d/%d desc %q images %d", soup.Revision, soup.ImportRevision, soup.Description, len(soup.Images))
	}
	if got, _ := b.st.GetRecipeByImportKey(ctx, seeded["legacy"].ImportKey); got.Notes != "Edited in the kitchen." {
		t.Errorf("the human edit was overwritten: %q", got.Notes)
	}
	// The update is itself "as imported", so the same bundle is now a no-op
	// for the soup, while the cheese ball still conflicts.
	wantCounts(t, b.importBytes(t, bundle, false), Counts{SkippedConflict: 1, Unchanged: 2})

	// A trashed match is a conflict, not a twin.
	if err := b.svc.Delete(ctx, soup.ID); err != nil {
		t.Fatal(err)
	}
	rep = b.importBytes(t, bundle, false)
	wantCounts(t, rep, Counts{SkippedConflict: 2, Unchanged: 1})
	if b.count(t) != 2 {
		t.Errorf("live recipes = %d, want 2", b.count(t))
	}
}

func TestInvalidItemsAndWarnings(t *testing.T) {
	in := newInstance(t)
	png := transparentPNG(t)
	shaOK := strings.Repeat("a", 64)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(exportfmt.RecipesFile)
	for _, line := range []string{
		`{"@type":"Recipe","name":"Example Good","x-grimoire":{"importKey":"k1"},"image":["images/` + shaOK + `.png","images/` + strings.Repeat("b", 64) + `.jpg","https://example.com/x.jpg"]}`,
		`not json`,
		`{"@type":"Recipe","name":""}`,
		`{"@type":"Recipe","name":"Example Dup","x-grimoire":{"importKey":"k1"}}`,
		`{"@type":"Person","name":"Not a recipe"}`,
		`{"@type":"Recipe","name":"Example Bad URL","url":"ftp://example.com/x"}`,
		`{"@type":"Recipe","name":"Example Foreign","url":"https://example.org/foreign","recipeIngredient":["1 egg"]}`,
		`{"@type":"Recipe","name":"Example Foreign Again","url":"https://example.org/foreign"}`,
	} {
		_, _ = io.WriteString(w, line+"\n")
	}
	iw, _ := zw.Create("images/" + shaOK + ".png")
	_, _ = iw.Write(png)
	_ = zw.Close()

	rep := in.importBytes(t, buf.Bytes(), false)
	wantCounts(t, rep, Counts{Created: 2, Invalid: 6})
	byIndex := map[int]ItemReport{}
	for _, it := range rep.Items {
		byIndex[it.Index] = it
	}
	if it := byIndex[0]; it.Outcome != Created || len(it.Warnings) != 2 {
		t.Errorf("item 0: %+v", it)
	}
	if it := byIndex[2]; it.Outcome != Invalid || it.Reasons[0] != "title: is required" {
		t.Errorf("item 2: %+v", it)
	}
	if it := byIndex[3]; it.Outcome != Invalid || !strings.Contains(it.Reasons[0], "duplicate of item 0") {
		t.Errorf("item 3: %+v", it)
	}
	if it := byIndex[5]; it.Outcome != Invalid {
		t.Errorf("item 5: %+v", it)
	}
	if it := byIndex[7]; it.Outcome != Invalid || !strings.Contains(it.Reasons[0], "duplicate of item 6") {
		t.Errorf("item 7: %+v", it)
	}
	good, err := in.st.GetRecipeByImportKey(context.Background(), "k1")
	if err != nil {
		t.Fatal(err)
	}
	if len(good.Images) != 1 || good.Images[0].MIME != "image/png" || good.SourceKind != recipe.SourceBundle {
		t.Errorf("good recipe: images %+v kind %q", good.Images, good.SourceKind)
	}
	// The foreign recipe is keyed by its URL, so importing it again
	// finds it instead of creating a twin.
	again := in.importBytes(t, buf.Bytes(), false)
	wantCounts(t, again, Counts{Unchanged: 2, Invalid: 6})
}
