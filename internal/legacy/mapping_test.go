// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"reflect"
	"strings"
	"testing"
)

const testMapCSV = `token,class,canonical,comment
# comment line
Desserts,category,,
Main Dishes,category,Main Dishes,
Cookies,subcategory,Cookies,
Aunt Example,contributor,Aunt Example,
Aunt Exmple,contributor,Aunt Example,variant
Chef Example,published_source,,
Microwave,tag,,
Sugar free,tag,Sugar-free,
Holiday,note,Served at holidays,
Glaze,group_label,Glaze,
Microwave/Cousin Sample,compound,tag=Microwave; contributor=Cousin Sample,
Breakfast Dish,category,Breakfast,
Breakfast Dish Ideas,note,Ideas,
`

func testMap(t *testing.T) *Map {
	t.Helper()
	m, err := LoadMap(strings.NewReader(testMapCSV))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLoadMap(t *testing.T) {
	m := testMap(t)
	cases := []struct {
		token string
		want  []Assignment
	}{
		{"desserts", []Assignment{{ClassCategory, "Desserts"}}}, // canonical defaults to the token
		{"  AUNT   exmple ", []Assignment{{ClassContributor, "Aunt Example"}}},
		{"Sugar free", []Assignment{{ClassTag, "Sugar-free"}}},
		{"Microwave/Cousin Sample", []Assignment{{ClassTag, "Microwave"}, {ClassContributor, "Cousin Sample"}}},
	}
	for _, c := range cases {
		e := m.Lookup(c.token)
		if e == nil {
			t.Errorf("Lookup(%q) = nil", c.token)
			continue
		}
		if !reflect.DeepEqual(e.Parts, c.want) {
			t.Errorf("Lookup(%q).Parts = %v, want %v", c.token, e.Parts, c.want)
		}
	}
	if m.Lookup("Uncle Nobody") != nil {
		t.Error("unknown token found")
	}
	if m.Lookup("Microwave/Cousin Sample").isCategory() || !m.Lookup("Desserts").isCategory() {
		t.Error("isCategory wrong")
	}
}

func TestLoadMapErrors(t *testing.T) {
	for name, in := range map[string]string{
		"bad class":      "Desserts,dessert,,\n",
		"duplicate":      "Desserts,category,,\ndesserts,category,,\n",
		"bad compound":   "X,compound,tag Microwave,\n",
		"nested":         "X,compound,compound=Y,\n",
		"empty compound": "X,compound,;,\n",
	} {
		if _, err := LoadMap(strings.NewReader(in)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSplitWordsGreedy(t *testing.T) {
	m := testMap(t)
	got := m.splitWords("Breakfast Dish Ideas Aunt Example Uncle Nobody Microwave")
	var texts []string
	var known []bool
	for _, g := range got {
		texts = append(texts, g.Text)
		known = append(known, g.Entry != nil)
	}
	wantTexts := []string{"Breakfast Dish Ideas", "Aunt Example", "Uncle Nobody", "Microwave"}
	wantKnown := []bool{true, true, false, true}
	if !reflect.DeepEqual(texts, wantTexts) || !reflect.DeepEqual(known, wantKnown) {
		t.Errorf("splitWords = %q %v, want %q %v", texts, known, wantTexts, wantKnown)
	}
}

func TestClassify(t *testing.T) {
	m := testMap(t)
	cases := []struct {
		fields      string
		cats        [][]string
		tags        []string
		contributor []string
		authors     []string
		notes       []string
		reasons     []string
	}{
		{
			fields: "Desserts\tCookies\tAunt Exmple\tHoliday",
			cats:   [][]string{{"Desserts", "Cookies"}}, contributor: []string{"Aunt Example"},
			notes: []string{"Served at holidays"},
		},
		{
			fields: "Main Dishes Microwave/Cousin Sample Chef Example",
			cats:   [][]string{{"Main Dishes"}}, tags: []string{"Microwave"},
			contributor: []string{"Cousin Sample"}, authors: []string{"Chef Example"},
		},
		{
			fields:  "Desserts\tNeighbor Invented",
			cats:    [][]string{{"Desserts"}},
			reasons: []string{"unclassified_token:Neighbor Invented"},
		},
		{
			fields:  "Desserts Neighbor Invented Microwave",
			cats:    [][]string{{"Desserts"}},
			tags:    []string{"Microwave"},
			reasons: []string{"unsplit_fields:Neighbor Invented"},
		},
		{
			fields:  "Cookies",
			cats:    [][]string{{"Cookies"}},
			reasons: []string{"subcategory_without_category:Cookies"},
		},
	}
	for _, c := range cases {
		a := classify(fieldTokens(c.fields, m))
		if !reflect.DeepEqual(a.categories, c.cats) || !reflect.DeepEqual(a.tags, c.tags) ||
			!reflect.DeepEqual(a.contributors, c.contributor) || !reflect.DeepEqual(a.authors, c.authors) ||
			!reflect.DeepEqual(a.notes, c.notes) || !reflect.DeepEqual(a.reasons, c.reasons) {
			t.Errorf("classify(%q) = %+v", c.fields, a)
		}
	}
}

func TestTokens(t *testing.T) {
	m := testMap(t)
	text := "CAKE\tDesserts\tAunt Example\nmix\nPIE\tDesserts\tUncle Nobody\nBUNS\tBread Things\tAunt Example\n"
	got := Tokens(text, FormatTabbed, m)
	want := []TokenCount{
		{"Aunt Example", 2, "contributor", "Aunt Example"},
		{"Desserts", 2, "category", "Desserts"},
		{"Bread Things", 1, "UNKNOWN", ""}, // found by the loose title rules
		{"Uncle Nobody", 1, "UNKNOWN", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokens = %+v\nwant %+v", got, want)
	}
	var b strings.Builder
	if err := WriteTokensCSV(&b, got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), "token,count,class,canonical\nAunt Example,2,contributor,Aunt Example\n") {
		t.Errorf("CSV = %q", b.String())
	}
}
