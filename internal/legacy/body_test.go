// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"reflect"
	"strings"
	"testing"
)

func linesOf(s string) []srcLine { return scanLines(s) }

func TestIsProse(t *testing.T) {
	cases := map[string]bool{
		"1 cup sugar":                         false,
		"1 pkg. (8 oz.)":                      false, // abbreviations, not sentence ends
		"2 lb. ground beef":                   false,
		"salt and pepper":                     false,
		"Mix well.":                           true,
		"Serve warm!":                         true,
		`Say "done."`:                         true,
		"Bake at 350 (until brown.)":          true,
		"Mix well. Add the eggs and stir":     true, // a sentence ends inside the line
		"1 pkg. Jello, any flavor":            false,
		strings.Repeat("flour and sugar ", 5): true, // over 75 characters
	}
	for in, want := range cases {
		if got := isProse(in); got != want {
			t.Errorf("isProse(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestMultiColumn(t *testing.T) {
	cases := []struct {
		line   string
		format Format
		want   bool
	}{
		{"For 3 dozen:\tfor 1 dozen:", FormatTabbed, true},
		{"For 3 dozen: for 1 dozen:", FormatPDFText, true},
		{"Single batch:\tDouble batch:", FormatTabbed, true},
		{"1 cup corn meal\t1/3 cup", FormatTabbed, true},
		{"1 cup corn meal 1/3 cup", FormatPDFText, true},
		{"3 eggs 1 egg", FormatPDFText, true},
		{"2 cups water 1", FormatPDFText, true},
		{"1 cup\tsugar", FormatTabbed, false}, // a tab between quantity and item
		{"1 1/2 cups milk", FormatPDFText, false},
		{"1 can (15 oz)", FormatPDFText, false},
		{"2 eggs, beaten", FormatPDFText, false},
		{"Bake 350", FormatPDFText, false},
		{"1 cup corn meal 1/3 cup", FormatTabbed, false}, // only PDF text collapses columns
	}
	for _, c := range cases {
		if got := isMultiColumn(c.line, c.format); got != c.want {
			t.Errorf("isMultiColumn(%q, %v) = %v, want %v", c.line, c.format, got, c.want)
		}
	}
}

type bodyWant struct {
	groups  map[string][]string // group name → raw lines; "" for unnamed
	order   []string            // group names in order
	steps   []string
	reasons []string
}

func checkBody(t *testing.T, name string, b body, w bodyWant) {
	t.Helper()
	var order []string
	got := map[string][]string{}
	for _, g := range b.groups {
		order = append(order, g.Name)
		for _, in := range g.Ingredients {
			got[g.Name] = append(got[g.Name], in.Raw)
		}
	}
	if !reflect.DeepEqual(order, w.order) || !reflect.DeepEqual(got, w.groups) {
		t.Errorf("%s: groups %v %q, want %v %q", name, order, got, w.order, w.groups)
	}
	if !reflect.DeepEqual(b.steps, w.steps) {
		t.Errorf("%s: steps %q, want %q", name, b.steps, w.steps)
	}
	if !reflect.DeepEqual(b.reasons, w.reasons) {
		t.Errorf("%s: reasons %q, want %q", name, b.reasons, w.reasons)
	}
}

func TestParseBodyGroups(t *testing.T) {
	m := testMap(t)
	// A label splitting the ingredient list makes a named group.
	b := parseBody(linesOf("1 cup flour\n2 eggs\nFrosting:\n1 cup Sugar--Powdered\nMix it all.\n"), FormatTabbed, m, nil)
	checkBody(t, "split list", b, bodyWant{
		order:  []string{"", "Frosting"},
		groups: map[string][]string{"": {"1 cup flour", "2 eggs"}, "Frosting": {"1 cup powdered sugar"}},
		steps:  []string{"Mix it all."},
	})

	// A label at the top names the first group; "For the filling:" →
	// "Filling"; a map group_label counts too.
	b = parseBody(linesOf("For the filling:\n1 can cherries\nGlaze:\n1 tbsp milk\nBake.\n"), FormatTabbed, m, nil)
	checkBody(t, "top label", b, bodyWant{
		order:  []string{"Filling", "Glaze"},
		groups: map[string][]string{"Filling": {"1 can cherries"}, "Glaze": {"1 tbsp milk"}},
		steps:  []string{"Bake."},
	})

	// A label inside the instructions stays in the text; it runs into the
	// paragraph it introduces. Followed by prose: no flag.
	b = parseBody(linesOf("1 lb beef\nBrown the beef.\nSPECIAL SAUCE:\nStir the soup until smooth.\n"), FormatTabbed, m, nil)
	checkBody(t, "label in steps", b, bodyWant{
		order:  []string{""},
		groups: map[string][]string{"": {"1 lb beef"}},
		steps:  []string{"Brown the beef.", "SPECIAL SAUCE: Stir the soup until smooth."},
	})

	// ...but followed by quantity lines it looks like a second ingredient
	// list: flagged, text kept.
	b = parseBody(linesOf("1 lb beef\nBrown the beef.\nTopping:\n1 cup crumbs\nSprinkle on top.\n"), FormatTabbed, m, nil)
	checkBody(t, "second list in steps", b, bodyWant{
		order:   []string{""},
		groups:  map[string][]string{"": {"1 lb beef"}},
		steps:   []string{"Brown the beef.", "Topping: 1 cup crumbs", "Sprinkle on top."},
		reasons: []string{ReasonGroupAmbiguous},
	})

	// An inline label keeps its line whole and is flagged.
	b = parseBody(linesOf("1 cup rice\nTopping: 1/2 cup crumbs\nBake.\n"), FormatTabbed, m, nil)
	checkBody(t, "inline label", b, bodyWant{
		order:   []string{""},
		groups:  map[string][]string{"": {"1 cup rice", "Topping: 1/2 cup crumbs"}},
		steps:   []string{"Bake."},
		reasons: []string{ReasonGroupAmbiguous},
	})

	// A label with no ingredient after it goes to the instructions, flagged.
	b = parseBody(linesOf("1 cup rice\nSauce:\nSimmer everything together.\n"), FormatTabbed, m, nil)
	checkBody(t, "dangling label", b, bodyWant{
		order:   []string{""},
		groups:  map[string][]string{"": {"1 cup rice"}},
		steps:   []string{"Sauce: Simmer everything together."},
		reasons: []string{ReasonGroupAmbiguous},
	})

	// An unknown label word is not trusted as a group.
	b = parseBody(linesOf("1 cup rice\nOptional:\n1 cup peas\nCook.\n"), FormatTabbed, m, nil)
	checkBody(t, "unknown label", b, bodyWant{
		order:   []string{""},
		groups:  map[string][]string{"": {"1 cup rice", "Optional:", "1 cup peas"}},
		steps:   []string{"Cook."},
		reasons: []string{ReasonGroupAmbiguous},
	})
}

func TestParseBodyPDF(t *testing.T) {
	m := testMap(t)
	text := "1 cup flour\n\f[[PAGE 2]]\n2 eggs\nStir the flour and eggs together in a large bowl until there are no lumps\n" +
		"left, then let it rest.\nHeat a well-\nbuttered pan\nand cook.\n\nServe hot\n"
	b := parseBody(linesOf(text), FormatPDFText, m, nil)
	checkBody(t, "pdf", b, bodyWant{
		order:  []string{""},
		groups: map[string][]string{"": {"1 cup flour", "2 eggs"}},
		steps: []string{
			"Stir the flour and eggs together in a large bowl until there are no lumps left, then let it rest.",
			"Heat a well-buttered pan and cook.",
			"Serve hot",
		},
	})
}

func TestParseBodyEdges(t *testing.T) {
	m := testMap(t)
	// All short lines: no steps (a float).
	b := parseBody(linesOf("2 scoops ice cream\n1 can root beer\n"), FormatTabbed, m, nil)
	if len(b.steps) != 0 || len(b.groups) != 1 {
		t.Errorf("float: %+v", b)
	}
	// Prose first: no ingredients.
	b = parseBody(linesOf("Pour everything over ice and stir.\n"), FormatTabbed, m, nil)
	if len(b.groups) != 0 || len(b.steps) != 1 {
		t.Errorf("punch: %+v", b)
	}
	// Multi-column lines are kept raw and never parsed.
	b = parseBody(linesOf("For 2 dozen:\tfor 1 dozen:\n2 cups Flour--All Purpose\t1 cup\nBake.\n"), FormatTabbed, m, nil)
	ins := b.groups[0].Ingredients
	if ins[1].Raw != "2 cups Flour--All Purpose\t1 cup" || ins[1].ParseOK || b.reasons[0] != ReasonMultiColumn {
		t.Errorf("table: %+v %v", ins, b.reasons)
	}
}

func TestConvertTitleWrap(t *testing.T) {
	m := testMap(t)
	text := "CAKE Desserts Cookies Aunt Example\nChef Example Microwave\n1 cup flour\nBake it.\n"
	res := Convert(text, Options{Format: FormatPDFText, Map: m, Collection: "C"})
	r := res.Drafts[0].Recipe
	if r.Author != "Chef Example" || len(r.Tags) != 1 || r.IngredientCount() != 1 || r.NeedsReview {
		t.Errorf("wrapped title line: %+v", r)
	}
}

func TestConvertPossibleMissedTitle(t *testing.T) {
	m := testMap(t)
	// "Pies" is not a category in the map, so PIE is not split off; the
	// loose rules notice it.
	text := "CAKE\tDesserts\n1 cup flour\nBake it.\nAPPLE PIE\tPies\n2 apples\nBake.\n"
	res := Convert(text, Options{Map: m, Collection: "C"})
	if len(res.Drafts) != 1 || !hasReason(res.Drafts[0], ReasonPossibleTitle) {
		t.Errorf("drafts = %+v", res.Drafts)
	}
}

func TestImportKey(t *testing.T) {
	if got := ImportKey("example-cookbook", 42, "MOM'S CHEESE BALL"); got != "legacy:example-cookbook:0042:moms-cheese-ball" {
		t.Errorf("ImportKey = %q", got)
	}
	text := "CAKE\tDesserts\n1 cup flour\nBake.\nPIE\tDesserts\n1 crust\nBake.\n"
	opt := Options{Map: testMap(t), Collection: "Example Cookbook"}
	a, b := Convert(text, opt), Convert(text, opt)
	if a.Drafts[1].Recipe.ImportKey != "legacy:example-cookbook:0002:pie" || a.Drafts[1].Recipe.ImportKey != b.Drafts[1].Recipe.ImportKey {
		t.Errorf("keys %q %q", a.Drafts[1].Recipe.ImportKey, b.Drafts[1].Recipe.ImportKey)
	}
}
