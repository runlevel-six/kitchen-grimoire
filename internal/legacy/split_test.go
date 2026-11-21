// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"strings"
	"testing"
)

const splitMapCSV = `token,class,canonical,comment
Salads,category,,
Breads,category,,
Appetizers,category,,
Breakfast Dishes,category,,
Desserts,category,,
Main Dishes,category,,
Aunt Example,contributor,,
BBQ,category,BBQ,spelled in caps on purpose
`

func splitMap(t *testing.T) *Map {
	t.Helper()
	m, err := LoadMap(strings.NewReader(splitMapCSV))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseTitle(t *testing.T) {
	m := splitMap(t)
	cases := []struct {
		line      string
		ok        bool
		title     string
		fields    string
		uncertain bool
	}{
		{line: "CHEESE BALL\tAppetizers\tAunt Example", ok: true, title: "CHEESE BALL", fields: "Appetizers\tAunt Example"},
		{line: "CHEESE BALL Appetizers Aunt Example", ok: true, title: "CHEESE BALL", fields: "Appetizers Aunt Example"},
		{line: "MOM'S \"BEST\" 7-UP CAKE #2 (OLD) Desserts", ok: true, title: "MOM'S \"BEST\" 7-UP CAKE #2 (OLD)", fields: "Desserts"},
		{line: "  SALADS Salads", ok: true, title: "SALADS", fields: "Salads"}, // the title may repeat the category
		{line: "PORK RIBS BBQ Aunt Example", ok: true, title: "PORK RIBS", fields: "BBQ Aunt Example"},
		{line: "pork ribs Main Dishes", ok: false},
		// Glued to the category: split before its capital, uncertain.
		{line: "LAYERED SALADSalads Aunt Example", ok: true, title: "LAYERED SALAD", fields: "Salads Aunt Example", uncertain: true},
		// Mixed-case suffix after an em dash.
		{line: "FRIED EGGS—easiest way Breakfast Dishes", ok: true, title: "FRIED EGGS—easiest way", fields: "Breakfast Dishes", uncertain: true},
		{line: "FRIED EGGS—easiest way\tBreakfast Dishes", ok: true, title: "FRIED EGGS—easiest way", fields: "Breakfast Dishes"},
		{line: "FRIED EGGS (the easy way) Breakfast Dishes", ok: true, title: "FRIED EGGS (the easy way)", fields: "Breakfast Dishes", uncertain: true},
		{line: "FRIED EGGS—see note. Breakfast Dishes", ok: false},
		// Not titles.
		{line: "SPECIAL SAUCE:", ok: false},
		{line: "SPECIAL SAUCE Salads:", ok: false},
		{line: "BAKE at 350 for 20 minutes.", ok: false},
		{line: "NOTE Salads are best fresh.", ok: false}, // caps, then a category word, then prose: ends a sentence
		{line: "CAKES AND BREADS", ok: false},            // a heading: category in caps doesn't count
		{line: "1 cup flour", ok: false},
		{line: "A Salads", ok: false}, // one capital is not a title
		{line: "", ok: false},
	}
	for _, c := range cases {
		got, ok := parseTitle(c.line, m, false)
		if ok != c.ok {
			t.Errorf("parseTitle(%q) ok = %v, want %v (%+v)", c.line, ok, c.ok, got)
			continue
		}
		if !ok {
			continue
		}
		if got.title != c.title || got.fields != c.fields || got.uncertain != c.uncertain {
			t.Errorf("parseTitle(%q) = %+v, want title %q fields %q uncertain %v", c.line, got, c.title, c.fields, c.uncertain)
		}
	}
}

func TestParseTitleLoose(t *testing.T) {
	empty := NewMap()
	cases := []struct {
		line  string
		ok    bool
		title string
	}{
		{"CHEESE BALL\tAppetizers\tAunt Example", true, "CHEESE BALL"},
		{"CHEESE BALL Appetizers Aunt Example", true, "CHEESE BALL"},
		{"CHEESE BALL", false, ""},
		{"1 CUP Bisquick", false, ""},
		{"BAKE at 350.", false, ""},
		{"SPECIAL SAUCE: Combine", false, ""},
	}
	for _, c := range cases {
		got, ok := parseTitle(c.line, empty, true)
		if ok != c.ok || (ok && got.title != c.title) {
			t.Errorf("loose parseTitle(%q) = %+v, %v; want %q, %v", c.line, got, ok, c.title, c.ok)
		}
		if _, strict := parseTitle(c.line, empty, false); strict {
			t.Errorf("strict parseTitle(%q) with an empty map accepted it", c.line)
		}
	}
}

func TestSplit(t *testing.T) {
	m := splitMap(t)
	text := "Cover page\n\nIntro line\n\f[[PAGE 1]]\nCHEESE BALL Appetizers\n1 cup cheese\n" +
		"\f[[PAGE 2]]\nMix well.\nSPECIAL SAUCE:\nStir.\nRANCH BREAD\tBreads\r\n2 cups flour\r\nBake."
	d := Split(text, FormatAuto, m)
	if d.Format != FormatPDFText {
		t.Errorf("format = %v", d.Format)
	}
	if d.PrefixLines != 2 || d.Prefix != "Cover page\n\nIntro line\n\f[[PAGE 1]]\n" {
		t.Errorf("prefix = %q (%d lines)", d.Prefix, d.PrefixLines)
	}
	if len(d.Blocks) != 2 || d.Blocks[0].Title != "CHEESE BALL" || d.Blocks[1].Title != "RANCH BREAD" {
		t.Fatalf("blocks = %+v", d.Blocks)
	}
	if d.Blocks[1].Ordinal != 2 {
		t.Errorf("ordinal = %d", d.Blocks[1].Ordinal)
	}
	// The page marker stays in Raw but not in the visible text.
	got := visibleText(append([]srcLine{d.Blocks[0].head}, d.Blocks[0].lines...))
	if got != "CHEESE BALL Appetizers\n1 cup cheese\nMix well.\nSPECIAL SAUCE:\nStir." {
		t.Errorf("visible text = %q", got)
	}
	var all strings.Builder
	all.WriteString(d.Prefix)
	for _, b := range d.Blocks {
		all.WriteString(b.Raw)
	}
	if all.String() != text {
		t.Errorf("prefix + blocks don't rebuild the input")
	}
}

func TestDetectFormat(t *testing.T) {
	cases := map[string]Format{
		"A\tB\nC":              FormatTabbed,
		"A\tB\n\f\nC":          FormatTabbed, // a Word page break is not a PDF marker
		"\f[[PAGE 1]]\nA\tB\n": FormatPDFText,
		"CAKE Desserts\nmix\n": FormatPDFText,
		"line one\rline two\r": FormatPDFText,
		"[[PAGE 12]]\nplain\n": FormatPDFText,
	}
	for in, want := range cases {
		if got := DetectFormat(in); got != want {
			t.Errorf("DetectFormat(%q) = %v, want %v", in, got, want)
		}
	}
	for _, s := range []string{"tabbed", "PDFTEXT", "auto", ""} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("ParseFormat(%q): %v", s, err)
		}
	}
	if _, err := ParseFormat("docx"); err == nil {
		t.Error("ParseFormat(docx) accepted")
	}
}

func TestScanLines(t *testing.T) {
	text := "a\r\nb\rc\n\fd"
	ls := scanLines(text)
	var got []string
	for _, l := range ls {
		got = append(got, l.text)
	}
	if strings.Join(got, "|") != "a|b|c|d" {
		t.Errorf("lines = %q", got)
	}
	if ls[len(ls)-1].end != len(text) || ls[1].start != 3 {
		t.Errorf("spans wrong: %+v", ls)
	}
}
