// SPDX-License-Identifier: Apache-2.0

package ingredient

import (
	"math"
	"strings"
	"testing"
)

// ok builds the expected result for a line that parses. Raw is filled in by
// the test from the input.
func ok(lo, hi float64, size, unit, item, note string) Line {
	return Line{ParseOK: true, QtyMin: lo, QtyMax: hi, Size: size, Unit: unit, Item: item, Note: note}
}

// free is the expected result for a line that must stay free text.
var free = Line{}

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Line
	}{
		// Plain numbers.
		{"2 cups flour", ok(2, 2, "", "cups", "flour", "")},
		{"1 cup butter", ok(1, 1, "", "cup", "butter", "")},
		{"1.5 cups milk", ok(1.5, 1.5, "", "cups", "milk", "")},
		{".5 tsp salt", ok(0.5, 0.5, "", "tsp", "salt", "")},
		{"12 oz pasta", ok(12, 12, "", "oz", "pasta", "")},

		// Fractions and mixed numbers.
		{"1/2 cup sugar", ok(0.5, 0.5, "", "cup", "sugar", "")},
		{"1 1/2 cups sugar", ok(1.5, 1.5, "", "cups", "sugar", "")},
		{"2 3/4 cups water", ok(2.75, 2.75, "", "cups", "water", "")},
		{"3/0 cup sugar", free},
		{"1 1/0 cups sugar", free},

		// Unicode fractions, alone and glued.
		{"½ cup sugar", ok(0.5, 0.5, "", "cup", "sugar", "")},
		{"¾ tsp baking soda", ok(0.75, 0.75, "", "tsp", "baking soda", "")},
		{"⅓ cup oil", ok(1.0/3, 1.0/3, "", "cup", "oil", "")},
		{"⅛ tsp nutmeg", ok(0.125, 0.125, "", "tsp", "nutmeg", "")},
		{"1½ cups sugar", ok(1.5, 1.5, "", "cups", "sugar", "")},
		{"2⅔ cups oats", ok(2+2.0/3, 2+2.0/3, "", "cups", "oats", "")},
		{"1 ½ cups sugar", ok(1.5, 1.5, "", "cups", "sugar", "")},

		// Ranges.
		{"3-4 Tbsp milk", ok(3, 4, "", "Tbsp", "milk", "")},
		{"3–4 Tbsp milk", ok(3, 4, "", "Tbsp", "milk", "")},
		{"3 - 4 Tbsp milk", ok(3, 4, "", "Tbsp", "milk", "")},
		{"3 to 4 Tbsp milk", ok(3, 4, "", "Tbsp", "milk", "")},
		{"1/2-1 tsp chili flakes", ok(0.5, 1, "", "tsp", "chili flakes", "")},
		{"1½–2 cups broth", ok(1.5, 2, "", "cups", "broth", "")},
		{"2 tomatoes", ok(2, 2, "", "", "tomatoes", "")}, // "to" inside a word is not a range

		// t vs T, and case-insensitivity elsewhere.
		{"1 t vanilla", ok(1, 1, "", "t", "vanilla", "")},
		{"1 T butter", ok(1, 1, "", "T", "butter", "")},
		{"2 TBSP honey", ok(2, 2, "", "TBSP", "honey", "")},
		{"1 Cup rice", ok(1, 1, "", "Cup", "rice", "")},
		{"2 tbs oil", ok(2, 2, "", "tbs", "oil", "")},
		{"1 c milk", ok(1, 1, "", "c", "milk", "")},

		// Trailing periods.
		{"1 tsp. salt", ok(1, 1, "", "tsp.", "salt", "")},
		{"2 Tbsp. sugar", ok(2, 2, "", "Tbsp.", "sugar", "")},
		{"8 oz. cheese", ok(8, 8, "", "oz.", "cheese", "")},
		{"2 pkgs. yeast", ok(2, 2, "", "pkgs.", "yeast", "")},

		// More units.
		{"3 cloves garlic, minced", ok(3, 3, "", "cloves", "garlic", "minced")},
		{"1 pinch cayenne", ok(1, 1, "", "pinch", "cayenne", "")},
		{"2 dashes bitters", ok(2, 2, "", "dashes", "bitters", "")},
		{"1 pint cream", ok(1, 1, "", "pint", "cream", "")},
		{"2 qt stock", ok(2, 2, "", "qt", "stock", "")},
		{"1 gal water", ok(1, 1, "", "gal", "water", "")},
		{"1 bunch parsley", ok(1, 1, "", "bunch", "parsley", "")},
		{"2 sticks butter", ok(2, 2, "", "sticks", "butter", "")},
		{"1 envelope gelatin", ok(1, 1, "", "envelope", "gelatin", "")},
		{"1 jar salsa", ok(1, 1, "", "jar", "salsa", "")},
		{"1 box pasta", ok(1, 1, "", "box", "pasta", "")},
		{"1 bag spinach", ok(1, 1, "", "bag", "spinach", "")},
		{"4 slices bread", ok(4, 4, "", "slices", "bread", "")},
		{"2 sprigs thyme", ok(2, 2, "", "sprigs", "thyme", "")},
		{"2 lbs potatoes", ok(2, 2, "", "lbs", "potatoes", "")},
		{"1 pound beef", ok(1, 1, "", "pound", "beef", "")},
		{"250 g flour", ok(250, 250, "", "g", "flour", "")},
		{"1 kg rice", ok(1, 1, "", "kg", "rice", "")},
		{"200 mL milk", ok(200, 200, "", "mL", "milk", "")},
		{"1 L water", ok(1, 1, "", "L", "water", "")},
		{"2 litres stock", ok(2, 2, "", "litres", "stock", "")},
		{"500g flour", ok(500, 500, "", "g", "flour", "")}, // unit glued to the number
		{"1 cup of flour", ok(1, 1, "", "cup", "flour", "")},

		// Sizes.
		{"1 8-oz pkg cream cheese, softened", ok(1, 1, "8-oz", "pkg", "cream cheese", "softened")},
		{"2 8 oz cans tomato sauce", ok(2, 2, "8 oz", "cans", "tomato sauce", "")},
		{"1 (15 oz) can black beans, drained", ok(1, 1, "(15 oz)", "can", "black beans", "drained")},
		{"2 (15-ounce) cans chickpeas", ok(2, 2, "(15-ounce)", "cans", "chickpeas", "")},
		{"1 14.5-oz can diced tomatoes", ok(1, 1, "14.5-oz", "can", "diced tomatoes", "")},
		{"1 1-lb bag carrots", ok(1, 1, "1-lb", "bag", "carrots", "")},
		{"1 8 oz steak", ok(1, 1, "", "", "8 oz steak", "")}, // space form needs a container unit

		// Notes.
		{"1 onion, chopped", ok(1, 1, "", "", "onion", "chopped")},
		{"2 cups flour, sifted, divided", ok(2, 2, "", "cups", "flour", "sifted, divided")},
		{"1 cup walnuts,chopped", ok(1, 1, "", "cup", "walnuts,chopped", "")}, // only ", " splits

		// Unit-less counts.
		{"2 eggs", ok(2, 2, "", "", "eggs", "")},
		{"3 large carrots", ok(3, 3, "", "", "large carrots", "")},

		// Legacy two-hyphen naming is left alone.
		{"1 cup Cheese--Cheddar", ok(1, 1, "", "cup", "Cheese--Cheddar", "")},

		// Raw is trimmed but otherwise kept.
		{"   2 eggs  ", ok(2, 2, "", "", "eggs", "")},

		// Free text: no leading quantity.
		{"salt and pepper to taste", free},
		{"pinch of salt", free},
		{"zest of 2 lemons", free},
		{"Filling:", free},
		{"", free},
		{"   ", free},

		// A quantity of nothing.
		{"2 cups", free},
		{"2 cups, sifted", free},
		{"1 (15 oz) can", free},
		{"3", free},

		// Nonsense quantities.
		{"0 cups sugar", free},
		{"-2 cups sugar", free},
		{"0/4 cup sugar", free},
		{"4-3 cups sugar", free},
		{"20000 cups sugar", free},
		{"1-20000 cups sugar", free},
		{"99999999999999999999999999999999999999 eggs", free},

		// The quantity must end at a word boundary.
		{"2-inch piece ginger", free},
		{"3eggs", free},
		{"2. Mix well", free},
	}
	for _, tt := range tests {
		got := Parse(tt.in)
		want := tt.want
		want.Raw = strings.TrimSpace(tt.in)
		if !equal(got, want) {
			t.Errorf("Parse(%q)\n got %+v\nwant %+v", tt.in, got, want)
		}
	}
}

// equal compares two ingredients, allowing for float rounding in thirds.
func equal(a, b Line) bool {
	near := func(x, y float64) bool { return math.Abs(x-y) < 1e-9 }
	qa, qb := a, b
	qa.QtyMin, qa.QtyMax, qb.QtyMin, qb.QtyMax = 0, 0, 0, 0
	return qa == qb && near(a.QtyMin, b.QtyMin) && near(a.QtyMax, b.QtyMax)
}

func TestParseRawPreserved(t *testing.T) {
	for in, want := range map[string]string{
		"  1 cup sugar\t": "1 cup sugar",
		"salt to taste ":  "salt to taste",
		"\n2 eggs\n":      "2 eggs",
	} {
		if got := Parse(in).Raw; got != want {
			t.Errorf("Parse(%q).Raw = %q, want %q", in, got, want)
		}
	}
}

func TestParseThenScale(t *testing.T) {
	for _, in := range []string{
		"1½ cups sugar", "3-4 Tbsp milk", "1 (15 oz) can black beans, drained",
		"2 eggs", "1 tsp. salt", "salt and pepper to taste",
	} {
		p := Parse(in)
		if got, _ := Scaled(p, 1); got != p.Raw {
			t.Errorf("Scaled(Parse(%q), 1) = %q, want raw %q", in, got, p.Raw)
		}
	}

	got, scaled := Scaled(Parse("1 8-oz pkg cream cheese, softened"), 2)
	if want := "2 8-oz pkg cream cheese, softened"; got != want || !scaled {
		t.Errorf("Scaled(…, 2) = %q %v, want %q true", got, scaled, want)
	}
	if got, _ := Scaled(Parse("2 8 oz cans tomato sauce"), 1.5); got != "3 8 oz cans tomato sauce" {
		t.Errorf("space-separated size must not scale: got %q", got)
	}
}

func TestCanonicalUnit(t *testing.T) {
	tests := map[string]string{
		"t": "tsp", "T": "tbsp", "tsp": "tsp", "tsp.": "tsp", "Teaspoons": "tsp",
		"Tbsp": "tbsp", "TBSP.": "tbsp", "tbs": "tbsp", "tablespoon": "tbsp",
		"c": "cup", "C": "cup", "cups": "cup",
		"oz": "oz", "oz.": "oz", "ounces": "oz", "lbs": "lb", "Pound": "lb",
		"pkg": "pkg", "pkgs.": "pkg", "package": "pkg",
		"cans": "can", "envelopes": "envelope", "sticks": "stick", "cloves": "clove",
		"pinches": "pinch", "dashes": "dash", "pt": "pint", "quarts": "quart", "qt": "quart",
		"gal": "gallon", "bunches": "bunch", "jars": "jar", "boxes": "box", "bags": "bag",
		"slices": "slice", "sprigs": "sprig", "g": "g", "grams": "g", "kg": "kg",
		"ml": "ml", "mL": "ml", "l": "l", "L": "l", "liters": "l", "litre": "l",
		"": "", "eggs": "", "large": "", "tt": "", "tsp..": "",
	}
	for in, want := range tests {
		if got := CanonicalUnit(in); got != want {
			t.Errorf("CanonicalUnit(%q) = %q, want %q", in, got, want)
		}
	}
}
