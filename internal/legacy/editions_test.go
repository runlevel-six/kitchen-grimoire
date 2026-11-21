// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"slices"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func TestNormalizeTitle(t *testing.T) {
	cases := map[string]string{
		"MAC & CHEESE":          "mac and cheese",
		"Mom’s  Best—Pie!":      "moms best pie",
		"  FRIED EGGS—easy way": "fried eggs easy way",
	}
	for in, want := range cases {
		if got := NormalizeTitle(in); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleSimilarity(t *testing.T) {
	same := [][2]string{
		{"CORN BREAD", "CORNBREAD"},
		{"POPPY SEED CAKE", "POPPYSEED CAKE"},
		{"LEMON POPPY SEED CAKE", "LEMON POPPYSEED CAKE"},
		{"CHOCOLATE CHIP COOKIES", "CHOC CHIP COOKIES"},
		{"CHOCOLATE CHIP COOKIES", "CHOCOLATE CHIP COOKIE"},
		{"MACARONI & CHEESE", "MACARONI AND CHEESE"},
		{"WHITE CHILI", "WHITE CHILLI"},
		{"CHOCOLATE CHIP OATMEAL COOKIES", "CHOCOLATE CHIP COOKIES"}, // 6/7: one word in four changed
	}
	for _, p := range same {
		if s := TitleSimilarity(p[0], p[1]); s < RenameThreshold {
			t.Errorf("TitleSimilarity(%q, %q) = %.2f, want ≥ %.2f", p[0], p[1], s, RenameThreshold)
		}
	}
	different := [][2]string{
		{"LEMON BARS", "LIME BARS"},
		{"CHOCOLATE CAKE", "CHOCOLATE CHIP CAKE"}, // 0.8
		{"BROWNIES", "BLONDIES"},
		{"CORN BREAD", "CORNMEAL MUFFINS"},
		{"7 LAYER DIP", "7 LAYER SALAD"},
		{"BEST EVER BROWNIES", "BROWNIES"},
		{"PIE", ""},
	}
	for _, p := range different {
		if s := TitleSimilarity(p[0], p[1]); s >= RenameThreshold {
			t.Errorf("TitleSimilarity(%q, %q) = %.2f, want < %.2f", p[0], p[1], s, RenameThreshold)
		}
	}
}

func draft(ord int, title string) Draft {
	return Draft{Ordinal: ord, Recipe: recipe.Recipe{Title: title}}
}

func TestMerge(t *testing.T) {
	primary := []Draft{
		draft(1, "CHEESE BALL"),
		draft(2, "CORNBREAD"),
		draft(3, "CHOC CHIP COOKIES"),
		draft(4, "SLOW COOKER CHILI"),
		draft(5, "PARTY PUNCH"),
	}
	older := []Draft{
		draft(1, "FIRST OLD ONLY"),
		draft(2, "Cheese Ball"),
		draft(3, "CORN BREAD"),
		draft(4, "PEANUT BUTTER FUDGE"),
		draft(5, "CHOCOLATE CHIP COOKIES"),
		draft(6, "PARTY PUNCH"),
		draft(7, "LAST OLD ONLY"),
	}
	got := Merge(primary, older)
	var titles []string
	for _, d := range got {
		titles = append(titles, d.Recipe.Title)
	}
	want := []string{"FIRST OLD ONLY", "CHEESE BALL", "CORNBREAD", "PEANUT BUTTER FUDGE",
		"CHOC CHIP COOKIES", "SLOW COOKER CHILI", "PARTY PUNCH", "LAST OLD ONLY"}
	if len(titles) != len(want) {
		t.Fatalf("titles = %q", titles)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("titles = %q\nwant %q", titles, want)
		}
	}
	check := func(i int, olderTitle string, reasons ...string) {
		t.Helper()
		d := got[i]
		if (d.Older == nil) != (olderTitle == "") || (d.Older != nil && d.Older.Recipe.Title != olderTitle) {
			t.Errorf("%s: older = %+v, want %q", d.Recipe.Title, d.Older, olderTitle)
		}
		if len(d.Recipe.ReviewReasons) != len(reasons) {
			t.Errorf("%s: reasons %q, want %q", d.Recipe.Title, d.Recipe.ReviewReasons, reasons)
			return
		}
		for j := range reasons {
			if d.Recipe.ReviewReasons[j] != reasons[j] {
				t.Errorf("%s: reasons %q, want %q", d.Recipe.Title, d.Recipe.ReviewReasons, reasons)
			}
		}
	}
	check(0, "", ReasonOlderOnly)
	check(1, "Cheese Ball") // case only: not a rename
	check(2, "CORN BREAD", ReasonTitleChanged)
	check(3, "", ReasonOlderOnly)
	check(4, "CHOCOLATE CHIP COOKIES", ReasonTitleChanged)
	check(5, "")
	check(6, "PARTY PUNCH")
	check(7, "", ReasonOlderOnly)
	if !got[0].OlderOnly || got[1].OlderOnly || got[1].Recipe.NeedsReview || !got[2].Recipe.NeedsReview {
		t.Error("OlderOnly / NeedsReview not set as expected")
	}
	// Merge must not modify its inputs.
	if primary[1].Older != nil || len(primary[1].Recipe.ReviewReasons) != 0 || len(older[0].Recipe.ReviewReasons) != 0 {
		t.Error("Merge modified its input")
	}
}

func TestMergeOneToOne(t *testing.T) {
	// Two older recipes compete for one primary title: the exact match
	// wins, the other stays older-only (and, coming first in the older
	// edition, is placed first).
	got := Merge([]Draft{draft(1, "CORNBREAD")}, []Draft{draft(1, "CORN BREAD"), draft(2, "CORNBREAD")})
	if len(got) != 2 || !got[0].OlderOnly || got[1].Older == nil || got[1].Older.Recipe.Title != "CORNBREAD" {
		t.Errorf("got %+v", got)
	}
}

func TestMergePairs(t *testing.T) {
	primary := []Draft{draft(1, "EXAMPLE ECLAIRS"), draft(2, "PARTY PUNCH")}
	older := []Draft{draft(1, "Example Eclair Pastry"), draft(2, "PARTY PUNCH")}
	if s := TitleSimilarity(older[0].Recipe.Title, primary[0].Recipe.Title); s >= RenameThreshold {
		t.Fatalf("test pair scores %.2f; it must be below the threshold", s)
	}
	pairs, err := LoadPairs(strings.NewReader("older,newer\n# confirmed by hand\nEXAMPLE ECLAIR PASTRY,Example Eclairs\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := MergePairs(primary, older, pairs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Older == nil || got[0].Older.Recipe.Title != "Example Eclair Pastry" ||
		!slices.Contains(got[0].Recipe.ReviewReasons, ReasonTitleChanged) || got[0].MatchScore >= RenameThreshold {
		t.Errorf("got %+v", got)
	}
	// Without the pair they stay apart.
	if got := Merge(primary, older); len(got) != 3 {
		t.Errorf("without pairs: %d drafts, want 3", len(got))
	}
}

func TestMergePairsMissing(t *testing.T) {
	pairs := Pairs{NormalizeTitle("NOT THERE"): NormalizeTitle("PARTY PUNCH")}
	_, err := MergePairs([]Draft{draft(1, "PARTY PUNCH")}, []Draft{draft(1, "PARTY PUNCH")}, pairs)
	if err == nil || !strings.Contains(err.Error(), "not there") {
		t.Errorf("err = %v, want the missing pair named", err)
	}
}

func TestLoadPairsErrors(t *testing.T) {
	for _, in := range []string{"A,\n", "ONLY ONE FIELD\n", "A,B\nA,C\n"} {
		if _, err := LoadPairs(strings.NewReader(in)); err == nil {
			t.Errorf("LoadPairs(%q): want an error", in)
		}
	}
}
