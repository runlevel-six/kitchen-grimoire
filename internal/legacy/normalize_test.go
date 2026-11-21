// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"strings"
	"testing"
)

func TestNormalizerRules(t *testing.T) {
	var n *Normalizer // nil: built-in rules only
	cases := map[string]string{
		"Cheese--Cheddar":                     "Cheddar cheese",
		"1 cup Cheese--Cheddar, shredded":     "1 cup Cheddar cheese, shredded",
		"2 cups Flour--All Purpose":           "2 cups all-purpose flour",
		"1 cup Sugar--Powdered":               "1 cup powdered sugar",
		"1 box Cake Mix--Lemon":               "1 box lemon cake mix",
		"1 cup Sugar—Brown, packed":           "1 cup brown sugar, packed",
		"1 can Beans--Kidney--Dark Red":       "1 can dark red kidney beans",
		"1 8-oz pkg Cheese--Cream (softened)": "1 8-oz pkg cream cheese (softened)",
		"1½ cups Flour--Self Rising":          "1½ cups self-rising flour",
		"stir — or whisk":                     "stir — or whisk", // a spaced em dash is punctuation
		"1 cup sugar":                         "1 cup sugar",
		"--Lemon":                             "--Lemon",
		"Cheese--":                            "Cheese--",
	}
	for in, want := range cases {
		got, changed := n.Line(in)
		if got != want || changed != (in != want) {
			t.Errorf("Line(%q) = %q, %v; want %q", in, got, changed, want)
		}
	}
}

func TestNormalizerOverrides(t *testing.T) {
	n, err := LoadNormalizer(strings.NewReader("from,to\n# comment\nCheese--American,American cheese slices\nSoup--Cream of Mushroom,cream of mushroom soup\n"))
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"8 slices cheese--american":    "8 slices American cheese slices",
		"1 can Soup—Cream of Mushroom": "1 can cream of mushroom soup",
		"1 cup Cheese--Swiss":          "1 cup Swiss cheese", // rules still apply
	} {
		if got, _ := n.Line(in); got != want {
			t.Errorf("Line(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := LoadNormalizer(strings.NewReader("a\n")); err == nil {
		t.Error("one-column row accepted")
	}
}
