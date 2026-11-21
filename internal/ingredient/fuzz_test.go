// SPDX-License-Identifier: Apache-2.0

package ingredient

import (
	"strings"
	"testing"
)

// FuzzParse checks that Parse never panics and that every line it accepts
// is sane (docs/DESIGN.md §15.2).
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"2 cups flour",
		"1 1/2 cups sugar",
		"1½ cups sugar",
		"3-4 Tbsp milk",
		"3 to 4 t salt",
		"1/2-1 tsp chili flakes",
		"1 8-oz pkg cream cheese, softened",
		"1 (15 oz) can black beans, drained",
		"2 8 oz cans tomato sauce",
		"1 onion, chopped",
		"salt and pepper to taste",
		"2 cups",
		"1/0 cup water",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := Parse(in)
		if got.Raw != strings.TrimSpace(in) {
			t.Fatalf("Raw = %q, want %q", got.Raw, strings.TrimSpace(in))
		}
		if !got.ParseOK {
			return
		}
		if got.Item == "" {
			t.Fatalf("Parse(%q): ParseOK with empty Item", in)
		}
		if !(0 < got.QtyMin && got.QtyMin <= got.QtyMax && got.QtyMax <= maxQty) {
			t.Fatalf("Parse(%q): bad quantity %v–%v", in, got.QtyMin, got.QtyMax)
		}
	})
}
