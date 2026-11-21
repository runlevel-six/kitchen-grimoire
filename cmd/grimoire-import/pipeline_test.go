// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/exportfmt"
	"github.com/runlevel-six/kitchen-grimoire/internal/imagestore"
	"github.com/runlevel-six/kitchen-grimoire/internal/ingest"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
	"github.com/runlevel-six/kitchen-grimoire/internal/store"
)

// The whole legacy path on the synthetic fixtures: convert (with the older
// edition) → bundle → import. Checks D-13: matched recipes keep the older
// edition as revision 1, older-only recipes are imported and flagged.
func TestConvertBundleImport(t *testing.T) {
	dir := t.TempDir()
	fx := filepath.Join("..", "..", "testdata", "legacy")
	var out, errOut bytes.Buffer
	if code := run([]string{"convert",
		"--in", filepath.Join(fx, "sample-newer.txt"),
		"--older", filepath.Join(fx, "sample-older.txt"),
		"--map", filepath.Join(fx, "attribution-map.csv"),
		"--normalize", filepath.Join(fx, "normalize.csv"),
		"--collection", "Example Family Cookbook",
		"--out", dir}, &out, &errOut); code != 0 {
		t.Fatalf("convert: %d %s", code, errOut.String())
	}
	zipPath := filepath.Join(dir, "bundle.zip")
	out.Reset()
	if code := run([]string{"bundle", "--drafts", filepath.Join(dir, "drafts.jsonl"), "--out", zipPath}, &out, &errOut); code != 0 {
		t.Fatalf("bundle: %d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "with an older edition") {
		t.Errorf("bundle output: %q", out.String())
	}

	ctx := context.Background()
	db, err := store.OpenDataDir(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	svc := recipe.NewService(st)
	files, _ := imagestore.New(t.TempDir())
	f, _ := os.Open(zipPath)
	defer f.Close()
	fi, _ := f.Stat()
	b, err := exportfmt.ReadBundle(f, fi.Size(), exportfmt.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := ingest.New(svc, st, files).ImportBundle(ctx, b, ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts.Invalid != 0 || rep.Counts.Created == 0 {
		t.Fatalf("report counts = %+v; items %+v", rep.Counts, rep.Items)
	}

	var twoRevs, olderOnly int
	for _, it := range rep.Items {
		rec, err := svc.Get(ctx, it.Slug)
		if err != nil {
			t.Fatalf("%s: %v", it.Title, err)
		}
		if strings.Contains(strings.Join(rec.ReviewReasons, ","), "only_in_older_edition") {
			olderOnly++
		}
		if rec.Revision == 2 {
			twoRevs++
			old, err := svc.Revision(ctx, rec.ID, 1)
			if err != nil || old.ImportKey != rec.ImportKey {
				t.Errorf("%s: revision 1 = %+v, %v", rec.Title, old, err)
			}
		}
	}
	if twoRevs == 0 {
		t.Error("no recipe kept its older edition as revision 1")
	}
	if olderOnly == 0 {
		t.Error("no older-only recipe was imported")
	}

	// The edited ingredient (pecans) shows the older wording in revision 1.
	found := false
	for _, it := range rep.Items {
		rec, _ := svc.Get(ctx, it.Slug)
		cur := ingredientsText(rec)
		if rec.Revision != 2 || !strings.Contains(strings.ToLower(cur), "pecans") {
			continue
		}
		old, _ := svc.Revision(ctx, rec.ID, 1)
		if ingredientsText(old) != cur {
			found = true
		}
	}
	if !found {
		t.Error("the edited ingredient does not differ between revisions 1 and 2")
	}

	// Re-importing the same bundle changes nothing.
	rep2, err := ingest.New(svc, st, files).ImportBundle(ctx, b, ingest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Counts.Created != 0 || rep2.Counts.Updated != 0 {
		t.Errorf("re-import counts = %+v", rep2.Counts)
	}
}

func ingredientsText(r *recipe.Recipe) string {
	var lines []string
	for _, g := range r.IngredientGroups {
		for _, in := range g.Ingredients {
			lines = append(lines, in.Raw)
		}
	}
	return strings.Join(lines, "\n")
}
