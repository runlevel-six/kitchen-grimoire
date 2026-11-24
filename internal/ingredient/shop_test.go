// SPDX-License-Identifier: Apache-2.0

package ingredient

import (
	"reflect"
	"strings"
	"testing"
)

func shopLines(recipe string, factor float64, raws ...string) []ShopLine {
	var out []ShopLine
	for _, r := range raws {
		out = append(out, ShopLine{Line: Parse(r), Factor: factor, Recipe: recipe})
	}
	return out
}

func findItem(items []ShopItem, name string) *ShopItem {
	for i := range items {
		if items[i].Item == name {
			return &items[i]
		}
	}
	return nil
}

func TestShopCombines(t *testing.T) {
	var lines []ShopLine
	lines = append(lines, shopLines("Pie", 1,
		"1 cup butter, softened",
		"2 eggs",
		"1 8-oz pkg cream cheese",
		"3 Tbsp sugar",
	)...)
	lines = append(lines, shopLines("Stuffing", 2,
		"½ cup butter",
		"1 egg",
		"2 tablespoons butter",
		"1 (8 oz) pkg cream cheese",
		"1 8-OZ package cream cheese",
		"3-4 cups chicken broth",
	)...)
	got := Shop(lines)

	tests := []struct {
		item    string
		amounts []string
		recipes []string
	}{
		{"butter", []string{"2 cups", "4 Tbsp"}, []string{"Pie", "Stuffing"}},
		{"eggs", []string{"4"}, []string{"Pie", "Stuffing"}},
		{"cream cheese", []string{"3 8-oz pkg", "2 (8 oz) pkg"}, []string{"Pie", "Stuffing"}},
		{"sugar", []string{"3 Tbsp"}, []string{"Pie"}},
		{"chicken broth", []string{"6–8 cups"}, []string{"Stuffing"}},
	}
	for _, tt := range tests {
		it := findItem(got.Items, tt.item)
		if it == nil {
			t.Errorf("missing %q in %+v", tt.item, got.Items)
			continue
		}
		if !reflect.DeepEqual(it.Amounts, tt.amounts) {
			t.Errorf("%s amounts = %q, want %q", tt.item, it.Amounts, tt.amounts)
		}
		if !reflect.DeepEqual(it.Recipes, tt.recipes) {
			t.Errorf("%s recipes = %q, want %q", tt.item, it.Recipes, tt.recipes)
		}
	}
	if len(got.Items) != len(tests) {
		t.Errorf("items = %+v, want %d rows", got.Items, len(tests))
	}
	if got.LineCount != 10 || got.CombinedFrom != 10 {
		t.Errorf("counts = %d lines, %d combined", got.LineCount, got.CombinedFrom)
	}
}

func TestShopKeepsEverything(t *testing.T) {
	lines := shopLines("Turkey", 1,
		"1 (12-14 lb) turkey",
		"Salt and pepper to taste",
		"pinch of salt",
		"2 tsp kosher salt",
		"4 cups water",
		"Butter for the pan",
		"a handful of parsley",
		"",
	)
	got := Shop(lines)
	if got.LineCount != 7 {
		t.Errorf("LineCount = %d, want 7 (blank lines skipped)", got.LineCount)
	}
	if findItem(got.Staples, "kosher salt") == nil || findItem(got.Staples, "water") == nil {
		t.Errorf("staples = %+v", got.Staples)
	}
	if len(got.StaplesRaw) != 2 || got.StaplesRaw[0].Raw != "Salt and pepper to taste" || got.StaplesRaw[1].Raw != "pinch of salt" {
		t.Errorf("staples raw = %+v", got.StaplesRaw)
	}
	var raws []string
	for _, u := range got.Unparsed {
		raws = append(raws, u.Raw)
		if u.Recipe != "Turkey" {
			t.Errorf("unparsed %q lost its recipe", u.Raw)
		}
	}
	if want := []string{"Butter for the pan", "a handful of parsley"}; !reflect.DeepEqual(raws, want) {
		t.Errorf("unparsed = %q, want %q", raws, want)
	}
	// Every non-blank line shows up somewhere.
	n := len(got.StaplesRaw) + len(got.Unparsed)
	for _, it := range append(got.Items, got.Staples...) {
		n += len(it.Recipes) // one recipe each here, one line each
	}
	if n != got.LineCount {
		t.Errorf("accounted for %d of %d lines", n, got.LineCount)
	}
}

func TestShopDifferentWordingStaysApart(t *testing.T) {
	got := Shop(shopLines("A", 1, "1 cup butter", "1 cup unsalted butter"))
	if len(got.Items) != 2 {
		t.Errorf("items = %+v; different wording must not merge", got.Items)
	}
}

func TestShopUnitlessPlural(t *testing.T) {
	got := Shop(shopLines("A", 1, "1 egg", "1 egg"))
	if it := findItem(got.Items, "egg"); it == nil || !reflect.DeepEqual(it.Amounts, []string{"2"}) {
		t.Errorf("items = %+v (no plural spelling seen, so keep singular)", got.Items)
	}
	got = Shop(shopLines("A", 0.5, "2 Eggs"))
	if it := findItem(got.Items, "Egg"); it == nil || !reflect.DeepEqual(it.Amounts, []string{"1"}) {
		t.Errorf("items = %+v (one egg reads as singular)", got.Items)
	}
	got = Shop(shopLines("A", 1, "1 egg", "2 eggs"))
	if it := findItem(got.Items, "eggs"); it == nil || !reflect.DeepEqual(it.Amounts, []string{"3"}) {
		t.Errorf("items = %+v (three eggs read as plural)", got.Items)
	}
}

func TestItemKey(t *testing.T) {
	for in, want := range map[string]string{
		"Eggs":             "egg",
		"  Green   Beans ": "green bean",
		"berries":          "berry",
		"tomatoes":         "tomato",
		"peaches":          "peach",
		"asparagus":        "asparagus",
		"hummus":           "hummus",
		"pea":              "pea",
		"glass":            "glass",
	} {
		if got := ItemKey(in); got != want {
			t.Errorf("ItemKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzShop(f *testing.F) {
	f.Add("2 cups flour\n1 egg\nsalt to taste\n1 8-oz pkg cream cheese", 2.0)
	f.Add("3-4 Tbsp milk\n½ cup milk", 0.5)
	f.Fuzz(func(t *testing.T, text string, factor float64) {
		var lines []ShopLine
		for raw := range strings.SplitSeq(text, "\n") {
			lines = append(lines, ShopLine{Line: Parse(raw), Factor: factor, Recipe: "R"})
		}
		got := Shop(lines)
		if got.CombinedFrom+len(got.Unparsed)+len(got.StaplesRaw) != got.LineCount {
			t.Fatalf("lines lost: %+v", got)
		}
	})
}
