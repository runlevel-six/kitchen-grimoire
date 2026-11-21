// SPDX-License-Identifier: Apache-2.0

package exportfmt

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func sha(c byte) string { return strings.Repeat(string(c), 64) }

// fullRecipe uses every field the format carries. All content is invented.
func fullRecipe() *recipe.Recipe {
	return &recipe.Recipe{
		ID:          "0190f1d2-0000-7000-8000-000000000001",
		Title:       "Example Cheese Ball & Crackers",
		Description: "Party staple.\n\nInvented for tests <3.",
		YieldText:   "1 ball",
		Servings:    12,
		PrepMin:     20,
		CookMin:     0,
		TotalMin:    140,
		IngredientGroups: []recipe.IngredientGroup{
			{Ingredients: []recipe.Ingredient{{Raw: "1 8-oz pkg cream cheese, softened"}, {Raw: "1 cup shredded cheddar"}}},
			{Name: "Coating", Ingredients: []recipe.Ingredient{{Raw: "½ cup chopped pecans"}}},
		},
		StepGroups: []recipe.StepGroup{
			{Name: "Ball", Steps: []string{"Beat cream cheese until smooth.", "Stir in cheddar."}},
			{Steps: []string{"Chill 2 hours."}},
			{Name: "Coating", Steps: []string{"Roll in pecans."}},
		},
		Notes:         "Aunt Example doubled the pecans.",
		Categories:    [][]string{{"Appetizers", "Spreads"}, {"Party"}},
		Tags:          []string{"make-ahead", "cheese, sharp"},
		SourceKind:    recipe.SourceLegacy,
		SourceURL:     "https://example.com/cheese-ball?ref=1",
		CanonicalURL:  "https://example.com/cheese-ball",
		SourceSite:    "example.com",
		Author:        "Example Chef",
		ContributedBy: "Example Contributor",
		Collection:    "Example Family Cookbook",
		ImportKey:     "legacy:example-collection:0042:example-cheese-ball",
		OriginalText:  "EXAMPLE CHEESE BALL  Appetizers  Example Contributor\n1 8-oz pkg cream cheese",
		RawSource:     json.RawMessage(`{ "@type": "Recipe", "name": "x" }`),
		NeedsReview:   true,
		ReviewReasons: []string{"unclassified_token:XYZ"},
		Images: []recipe.Image{
			{SHA256: sha('b'), MIME: "image/png", Width: 10, Height: 10},
			{SHA256: sha('a'), MIME: "image/jpeg", Width: 10, Height: 10, IsPrimary: true},
		},
		CreatedAt: time.Date(2020, 1, 2, 3, 4, 5, 6_000_000, time.UTC),
		UpdatedAt: time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func decode(t *testing.T, r *recipe.Recipe) Object {
	t.Helper()
	line, err := MarshalLine(r)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := decodeLine(line)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestToJSONLDShape(t *testing.T) {
	line, err := MarshalLine(fullRecipe())
	if err != nil {
		t.Fatal(err)
	}
	s := string(line)
	for _, want := range []string{
		`"@context":"https://schema.org"`,
		`"@type":"Recipe"`,
		`"name":"Example Cheese Ball & Crackers"`, // no & escaping
		`"prepTime":"PT20M"`,
		`"totalTime":"PT2H20M"`,
		`"recipeYield":"1 ball"`,
		`"recipeIngredient":["1 8-oz pkg cream cheese, softened","1 cup shredded cheddar","½ cup chopped pecans"]`,
		`{"@type":"HowToSection","itemListElement":[{"@type":"HowToStep","text":"Beat cream cheese until smooth."},{"@type":"HowToStep","text":"Stir in cheddar."}],"name":"Ball"},{"@type":"HowToStep","text":"Chill 2 hours."}`,
		`"recipeCategory":["Spreads","Party"]`,
		`"keywords":"make-ahead, cheese, sharp"`,
		`"author":{"@type":"Person","name":"Example Chef"}`,
		`"url":"https://example.com/cheese-ball?ref=1"`,
		// The primary image comes first.
		`"image":["images/` + sha('a') + `.jpg","images/` + sha('b') + `.png"]`,
		`"datePublished":"2020-01-02T03:04:05.006Z"`,
		`"dateModified":"2021-01-02T03:04:05.000Z"`,
		`"x-grimoire":{"version":1,"importKey":"legacy:example-collection:0042:example-cheese-ball","sourceKind":"legacy"`,
		`"categoryPath":[["Appetizers","Spreads"],["Party"]]`,
		`"ingredientGroups":[{"name":null,"lines":["1 8-oz pkg cream cheese, softened","1 cup shredded cheddar"]},{"name":"Coating","lines":["½ cup chopped pecans"]}]`,
		`"stepGroups":[{"name":"Ball","lines":["Beat cream cheese until smooth.","Stir in cheddar."]},{"name":null,"lines":["Chill 2 hours."]},{"name":"Coating","lines":["Roll in pecans."]}]`,
		`"rawSource":{"@type":"Recipe","name":"x"}`,
		`"servings":12`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s\nin %s", want, s)
		}
	}
	if strings.Contains(s, "cookTime") {
		t.Error("zero cookTime was written")
	}
	// Parsed ingredient fields are derived and never exported (D-06).
	if strings.Contains(s, "qtyMin") || strings.Contains(s, `"unit"`) {
		t.Error("parsed ingredient fields were exported")
	}
}

func TestRoundTripIsLossless(t *testing.T) {
	orig := fullRecipe()
	recipe.Normalize(orig)
	back, warnings, err := FromJSONLD(decode(t, orig))
	if err != nil || len(warnings) > 0 {
		t.Fatalf("FromJSONLD: %v %v", err, warnings)
	}
	recipe.Normalize(back)
	// Identity isn't carried; images come back as references in export order.
	back.ID = orig.ID
	if back.Images[0].SHA256 != sha('a') || back.Images[1].MIME != "image/png" {
		t.Errorf("images = %+v", back.Images)
	}
	a, _ := MarshalLine(orig)
	b, _ := MarshalLine(back)
	if string(a) != string(b) {
		t.Fatalf("round trip changed the recipe:\n%s\n%s", a, b)
	}
	if back.Tags[1] != "cheese, sharp" {
		t.Errorf("a tag with a comma was split: %q", back.Tags)
	}
	if !back.CreatedAt.Equal(orig.CreatedAt) {
		t.Errorf("created %v, want %v", back.CreatedAt, orig.CreatedAt)
	}
}

func TestSelfKey(t *testing.T) {
	r := fullRecipe()
	r.ImportKey = ""
	if k := ExportKey(r); k != "recipe:"+r.ID {
		t.Errorf("ExportKey = %q", k)
	}
	back, _, err := FromJSONLD(decode(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if back.ImportKey != "recipe:"+r.ID {
		t.Errorf("import key = %q", back.ImportKey)
	}
	if k := ExportKey(&recipe.Recipe{}); k != "" {
		t.Errorf("ExportKey of a new recipe = %q", k)
	}
}

func TestFromJSONLDForeign(t *testing.T) {
	// A schema.org Recipe from another app: no x-grimoire, HTML in text,
	// string instructions, a remote image, a comma-separated category.
	var obj Object
	if err := json.Unmarshal([]byte(`{
		"@type": ["Recipe", "NewsArticle"],
		"name": "Example <b>Soup</b>",
		"recipeIngredient": ["2 cups stock", "1 onion"],
		"recipeInstructions": "Simmer everything.",
		"recipeCategory": "Soups, Starters",
		"keywords": ["easy", "weeknight"],
		"recipeYield": 4,
		"cookTime": "PT1H",
		"author": [{"@type": "Person", "name": "A. Cook"}, "B. Cook"],
		"url": "https://example.org/soup",
		"image": [{"@type": "ImageObject", "url": "https://example.org/soup.jpg"}, "images/`+sha('c')+`.jpg"],
		"datePublished": "2019-05-06",
		"someOtherField": {"ignored": true}
	}`), &obj); err != nil {
		t.Fatal(err)
	}
	r, warnings, err := FromJSONLD(obj)
	if err != nil {
		t.Fatal(err)
	}
	if r.Title != "Example Soup" || r.CookMin != 60 || r.Servings != 4 || r.Author != "A. Cook, B. Cook" {
		t.Errorf("fields: %q %d %v %q", r.Title, r.CookMin, r.Servings, r.Author)
	}
	if r.SourceKind != recipe.SourceBundle || r.CanonicalURL != "https://example.org/soup" || r.ImportKey != "" {
		t.Errorf("provenance: %q %q %q", r.SourceKind, r.CanonicalURL, r.ImportKey)
	}
	if !slices.Equal(r.Tags, []string{"easy", "weeknight"}) || len(r.Categories) != 2 || r.Categories[1][0] != "Starters" {
		t.Errorf("tags %v categories %v", r.Tags, r.Categories)
	}
	if r.IngredientCount() != 2 || r.StepCount() != 1 {
		t.Errorf("ingredients %d steps %d", r.IngredientCount(), r.StepCount())
	}
	if len(r.Images) != 1 || r.Images[0].SHA256 != sha('c') {
		t.Errorf("images %+v", r.Images)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "soup.jpg") {
		t.Errorf("warnings = %v", warnings)
	}
	if want := time.Date(2019, 5, 6, 0, 0, 0, 0, time.UTC); !r.CreatedAt.Equal(want) {
		t.Errorf("created = %v", r.CreatedAt)
	}
}

func TestFromJSONLDPartialExtension(t *testing.T) {
	// x-grimoire without list fields falls back to the plain ones; its
	// scalars still win (no servings guessed from the yield).
	var obj Object
	_ = json.Unmarshal([]byte(`{"@type":"Recipe","name":"Example Bread",
		"recipeYield":"2 loaves","recipeIngredient":["3 cups flour"],
		"recipeCategory":["Breads"],
		"x-grimoire":{"version":2,"importKey":"legacy:x:1","sourceKind":"bogus","futureField":1}}`), &obj)
	r, warnings, err := FromJSONLD(obj)
	if err != nil {
		t.Fatal(err)
	}
	if r.IngredientCount() != 1 || len(r.Categories) != 1 || r.Servings != 0 || r.ImportKey != "legacy:x:1" {
		t.Errorf("got %+v", r)
	}
	if r.SourceKind != recipe.SourceBundle || len(warnings) != 2 {
		t.Errorf("kind %q warnings %v", r.SourceKind, warnings)
	}
}

func TestFromJSONLDErrors(t *testing.T) {
	for name, src := range map[string]string{
		"not a recipe":   `{"@type":"Person","name":"x"}`,
		"bad x-grimoire": `{"@type":"Recipe","name":"x","x-grimoire":{"ingredientGroups":"nope"}}`,
	} {
		var obj Object
		if err := json.Unmarshal([]byte(src), &obj); err != nil {
			t.Fatal(err)
		}
		if _, _, err := FromJSONLD(obj); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestDurations(t *testing.T) {
	for m, want := range map[int]string{1: "PT1M", 45: "PT45M", 60: "PT1H", 90: "PT1H30M", 60 * 30: "PT30H"} {
		if got := FormatDuration(m); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", m, got, want)
		}
		if back := ParseDuration(want); back != m {
			t.Errorf("ParseDuration(%q) = %d, want %d", want, back, m)
		}
	}
}

func TestParseImagePath(t *testing.T) {
	for p, ok := range map[string]bool{
		"images/" + sha('a') + ".jpg":                     true,
		"images/" + sha('a') + ".png":                     true,
		"images/" + sha('a') + ".gif":                     false,
		"images/" + strings.ToUpper(sha('a')) + ".jpg":    false,
		"images/" + sha('a')[:63] + ".jpg":                false,
		"images/../" + sha('a') + ".jpg":                  false,
		"img/" + sha('a') + ".jpg":                        false,
		"images/" + sha('a') + ".jpg.png":                 false,
		"https://example.com/images/" + sha('a') + ".jpg": false,
	} {
		if _, _, got := ParseImagePath(p); got != ok {
			t.Errorf("ParseImagePath(%q) ok = %v, want %v", p, got, ok)
		}
	}
}
