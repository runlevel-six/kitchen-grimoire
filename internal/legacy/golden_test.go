// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/legacy/golden/* from current output")

// fixtureDir holds the synthetic sample collection (invented recipes and
// names only, docs/DESIGN.md §17).
const fixtureDir = "../../testdata/legacy"

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fixtureOptions(t *testing.T) Options {
	t.Helper()
	mf, err := os.Open(filepath.Join(fixtureDir, "attribution-map.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer mf.Close()
	m, err := LoadMap(mf)
	if err != nil {
		t.Fatal(err)
	}
	nf, err := os.Open(filepath.Join(fixtureDir, "normalize.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer nf.Close()
	n, err := LoadNormalizer(nf)
	if err != nil {
		t.Fatal(err)
	}
	return Options{Map: m, Normalizer: n, Collection: "Example Family Cookbook"}
}

// checkGolden compares got with testdata/legacy/golden/name, or rewrites
// it with -update.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(fixtureDir, "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden; run `go test ./internal/legacy -update` and review the diff.\ngot:\n%s", name, got)
	}
}

// renderAll writes the three outputs of a conversion.
func renderAll(t *testing.T, rep *Report) (drafts, csv, md []byte) {
	t.Helper()
	var d, c, m bytes.Buffer
	if err := WriteDraftsJSONL(&d, rep.Drafts); err != nil {
		t.Fatal(err)
	}
	if err := rep.WriteCSV(&c); err != nil {
		t.Fatal(err)
	}
	if err := rep.WriteMarkdown(&m); err != nil {
		t.Fatal(err)
	}
	return d.Bytes(), c.Bytes(), m.Bytes()
}

// TestGoldenTabbed converts the older (word-processor) edition alone.
func TestGoldenTabbed(t *testing.T) {
	opt := fixtureOptions(t)
	res := Convert(readFixture(t, "sample-older.txt"), opt)
	if res.Format != FormatTabbed {
		t.Fatalf("detected format %v, want tabbed", res.Format)
	}
	rep := &Report{Collection: opt.Collection, Format: res.Format, PrefixLines: res.PrefixLines, Drafts: res.Drafts}
	d, c, m := renderAll(t, rep)
	checkGolden(t, "tabbed.drafts.jsonl", d)
	checkGolden(t, "tabbed.report.csv", c)
	checkGolden(t, "tabbed.report.md", m)
}

// TestGoldenMerged converts the newer (PDF text) edition as primary with
// the older edition merged in (D-13).
func TestGoldenMerged(t *testing.T) {
	opt := fixtureOptions(t)
	res := Convert(readFixture(t, "sample-newer.txt"), opt)
	if res.Format != FormatPDFText {
		t.Fatalf("detected format %v, want pdftext", res.Format)
	}
	oopt := opt
	oopt.KeySuffix = "older"
	ores := Convert(readFixture(t, "sample-older.txt"), oopt)
	rep := &Report{
		Collection: opt.Collection, Format: res.Format, PrefixLines: res.PrefixLines,
		HasOlder: true, OlderFormat: ores.Format, OlderPrefixLines: ores.PrefixLines,
		Drafts: Merge(res.Drafts, ores.Drafts),
	}
	d, c, m := renderAll(t, rep)
	checkGolden(t, "merged.drafts.jsonl", d)
	checkGolden(t, "merged.report.csv", c)
	checkGolden(t, "merged.report.md", m)
}

// TestFixtureFeatures spells out what the sample is meant to exercise, so
// a golden update that loses one of them fails here too.
func TestFixtureFeatures(t *testing.T) {
	opt := fixtureOptions(t)
	older := Convert(readFixture(t, "sample-older.txt"), opt)
	newer := Convert(readFixture(t, "sample-newer.txt"), opt)
	if len(older.Drafts) != 13 || len(newer.Drafts) != 13 {
		t.Fatalf("got %d older and %d newer recipes, want 13 each", len(older.Drafts), len(newer.Drafts))
	}
	if older.PrefixLines != 3 || newer.PrefixLines != 2 {
		t.Errorf("prefix lines = %d, %d; want 3, 2", older.PrefixLines, newer.PrefixLines)
	}
	byTitle := func(ds []Draft, title string) Draft {
		t.Helper()
		for _, d := range ds {
			if d.Recipe.Title == title {
				return d
			}
		}
		t.Fatalf("no recipe %q", title)
		return Draft{}
	}
	for _, ds := range [][]Draft{older.Drafts, newer.Drafts} {
		hc := byTitle(ds, "HAMBURGER CASSEROLE").Recipe
		if hc.ContributedBy != "Cousin Sample" || len(hc.Tags) != 1 || hc.Tags[0] != "Microwave" {
			t.Errorf("compound token: contributor %q tags %v", hc.ContributedBy, hc.Tags)
		}
		if hc.StepCount() != 3 || hc.NeedsReview {
			t.Errorf("inline SPECIAL SAUCE: steps %d, review %v", hc.StepCount(), hc.ReviewReasons)
		}
		cake := byTitle(ds, "LEMON POPPY SEED CAKE").Recipe
		if len(cake.IngredientGroups) != 2 || cake.IngredientGroups[1].Name != "Glaze" || cake.Author != "Chef Example" {
			t.Errorf("cake groups %+v author %q", cake.IngredientGroups, cake.Author)
		}
		if p := byTitle(ds, "PARTY PUNCH").Recipe; p.ContributedBy != "Aunt Example" || p.IngredientCount() != 0 {
			t.Errorf("punch: contributor %q (spelling variant should merge), %d ingredients", p.ContributedBy, p.IngredientCount())
		}
	}
	if !byTitle(newer.Drafts, "LAYERED SALAD").Recipe.NeedsReview {
		t.Error("glued title/category in PDF text should be flagged")
	}
}
