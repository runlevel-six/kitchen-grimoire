// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func TestFTSQuery(t *testing.T) {
	tests := map[string]string{
		"":                          "",
		"   ":                       "",
		"butter":                    `("butter"*)`,
		"cheese ball":               `("cheese" AND "ball"*)`,
		`"cream cheese" frosting`:   `("cream cheese" AND "frosting"*)`,
		"cookies -nuts":             `("cookies") NOT "nuts"`,
		"-nuts cookies":             `("cookies"*) NOT "nuts"`,
		"-nuts":                     "",
		`a"b`:                       `("a" AND "b")`,
		"crème brûlée":              `("crème" AND "brûlée"*)`,
		"AND OR NOT NEAR(":          `("AND" AND "OR" AND "NOT" AND "NEAR"*)`,
		`unterminated "phrase here`: `("unterminated" AND "phrase here")`,
		"x":                         `("x")`,
		"mom's":                     `("mom s")`, // tokenizes like the indexed text: mom, s
	}
	for in, want := range tests {
		if got := FTSQuery(in); got != want {
			t.Errorf("FTSQuery(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	mk := func(r *recipe.Recipe) *recipe.Recipe {
		t.Helper()
		if err := svc.Create(ctx, r, ""); err != nil {
			t.Fatal(err)
		}
		return r
	}
	ball := mk(&recipe.Recipe{
		Title:            "Example Cheese Ball",
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "1 8-oz pkg cream cheese"}, {Raw: "1 cup Cheese--Cheddar"}}}},
		StepGroups:       []recipe.StepGroup{{Steps: []string{"Mix and roll in nuts."}}},
		Categories:       [][]string{{"Appetizers"}},
		ContributedBy:    "Aunt Example",
		Collection:       "Example Family Cookbook",
	})
	mk(&recipe.Recipe{
		Title:            "Buttermilk Pancakes",
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "2 cups buttermilk"}}}},
		Categories:       [][]string{{"Breakfast Dishes"}},
		Tags:             []string{"Weekend"},
	})
	mk(&recipe.Recipe{
		Title:         "Crème Brûlée",
		Description:   "Serve with a little cheese plate on the side.",
		Categories:    [][]string{{"Desserts", "Custards"}},
		ContributedBy: "aunt example",
	})
	cookies := mk(&recipe.Recipe{
		Title:            "Nutty Cookies",
		Categories:       [][]string{{"Desserts", "Cookies"}},
		IngredientGroups: []recipe.IngredientGroup{{Ingredients: []recipe.Ingredient{{Raw: "1 cup walnuts"}}}},
	})

	titles := func(q SearchQuery) []string {
		t.Helper()
		res, total, err := st.Search(ctx, q)
		if err != nil {
			t.Fatalf("Search(%+v): %v", q, err)
		}
		if total != len(res) {
			t.Errorf("total %d != len %d", total, len(res))
		}
		var out []string
		for _, s := range res {
			out = append(out, s.Title)
		}
		return out
	}
	check := func(name string, q SearchQuery, want ...string) {
		t.Helper()
		got := titles(q)
		if len(got) != len(want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: got %q, want %q", name, got, want)
				return
			}
		}
	}

	check("title match outranks body match", SearchQuery{Text: "cheese"}, "Example Cheese Ball", "Crème Brûlée")
	check("legacy double-hyphen names are searchable", SearchQuery{Text: "cheddar"}, "Example Cheese Ball")
	check("prefix on the last word", SearchQuery{Text: "butter"}, "Buttermilk Pancakes")
	check("diacritics folded", SearchQuery{Text: "creme brulee"}, "Crème Brûlée")
	// "Example" is also in the cheese ball's title, which weighs most.
	check("contributor searchable", SearchQuery{Text: "aunt example"}, "Example Cheese Ball", "Crème Brûlée")
	check("phrase", SearchQuery{Text: `"cream cheese"`}, "Example Cheese Ball")
	check("exclusion", SearchQuery{Text: "cheese -plate"}, "Example Cheese Ball")
	check("category includes sub-categories", SearchQuery{Category: "desserts"}, "Crème Brûlée", "Nutty Cookies")
	check("sub-category only", SearchQuery{Category: "desserts/cookies"}, "Nutty Cookies")
	check("tag", SearchQuery{Tag: "weekend"}, "Buttermilk Pancakes")
	check("contributor filter is case-insensitive", SearchQuery{Contributor: "AUNT EXAMPLE"}, "Crème Brûlée", "Example Cheese Ball")
	check("collection filter", SearchQuery{Collection: "example family cookbook"}, "Example Cheese Ball")
	check("text plus filter", SearchQuery{Text: "cheese", Category: "appetizers"}, "Example Cheese Ball")
	check("nonsense finds nothing, no error", SearchQuery{Text: `NEAR( "" * ^`})
	check("no text lists A–Z", SearchQuery{}, "Buttermilk Pancakes", "Crème Brûlée", "Example Cheese Ball", "Nutty Cookies")

	// Edits reindex; trash hides; purge removes from the index.
	e, _ := svc.Get(ctx, cookies.Slug)
	e.Title = "Walnut Shortbread"
	if err := svc.Update(ctx, e, ""); err != nil {
		t.Fatal(err)
	}
	check("edit reindexed", SearchQuery{Text: "shortbread"}, "Walnut Shortbread")
	check("old title gone", SearchQuery{Text: "nutty"})
	if err := svc.Delete(ctx, ball.ID); err != nil {
		t.Fatal(err)
	}
	check("trashed hidden", SearchQuery{Text: "cheddar"})
	if _, err := svc.EmptyTrash(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = st.db.QueryRow(`SELECT count(*) FROM recipes_fts WHERE recipe_id = ?`, ball.ID).Scan(&n)
	if n != 0 {
		t.Errorf("purged recipe still indexed")
	}

	// Reindex rebuilds from scratch with the same results.
	if _, err := st.db.Exec(`DELETE FROM recipes_fts`); err != nil {
		t.Fatal(err)
	}
	if n, err := st.Reindex(ctx); err != nil || n != 3 {
		t.Fatalf("Reindex = %d, %v", n, err)
	}
	check("after reindex", SearchQuery{Text: "butter"}, "Buttermilk Pancakes")

	tree, err := st.CategoryTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range tree {
		got = append(got, c.Path)
	}
	// Appetizers is now empty (its only recipe was purged) and is omitted.
	want := []string{"breakfast-dishes", "desserts", "desserts/cookies", "desserts/custards"}
	if len(got) != len(want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tree = %v, want %v", got, want)
		}
	}
	if tree[1].Count != 2 || tree[1].Depth != 0 || tree[2].Depth != 1 {
		t.Errorf("desserts node = %+v", tree[1])
	}
	people, _ := st.Contributors(ctx)
	if len(people) != 1 || people[0].Count != 1 {
		t.Errorf("contributors = %+v", people)
	}
	tags, _ := st.Tags(ctx)
	if len(tags) != 1 || tags[0].Slug != "weekend" {
		t.Errorf("tags = %+v", tags)
	}
}
