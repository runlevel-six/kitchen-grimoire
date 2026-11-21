// SPDX-License-Identifier: Apache-2.0

package ingredient

import (
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

func TestFormatQty(t *testing.T) {
	tests := map[float64]string{
		1:       "1",
		0.5:     "½",
		1.5:     "1½",
		0.25:    "¼",
		0.75:    "¾",
		0.125:   "⅛",
		2.375:   "2⅜",
		1.0 / 3: "⅓",
		2.0 / 3: "⅔",
		0.99:    "1",
		0.01:    "⅛",
		12:      "12",
	}
	for in, want := range tests {
		if got := FormatQty(in); got != want {
			t.Errorf("FormatQty(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestScaled(t *testing.T) {
	pkg := recipe.Ingredient{
		Raw: "1 8-oz pkg cream cheese, softened", ParseOK: true,
		QtyMin: 1, QtyMax: 1, Size: "8-oz", Unit: "pkg", Item: "cream cheese", Note: "softened",
	}
	if got, ok := Scaled(pkg, 2); got != "2 8-oz pkg cream cheese, softened" || !ok {
		t.Errorf("size must not scale: got %q %v", got, ok)
	}

	rng := recipe.Ingredient{Raw: "3-4 Tbsp milk", ParseOK: true, QtyMin: 3, QtyMax: 4, Unit: "Tbsp", Item: "milk"}
	if got, _ := Scaled(rng, 0.5); got != "1½–2 Tbsp milk" {
		t.Errorf("range: got %q", got)
	}

	raw := recipe.Ingredient{Raw: "salt and pepper to taste"}
	if got, ok := Scaled(raw, 2); got != raw.Raw || ok {
		t.Errorf("unparsed line must be verbatim and unscaled: got %q %v", got, ok)
	}

	cup := recipe.Ingredient{Raw: "1 cup butter", ParseOK: true, QtyMin: 1, QtyMax: 1, Unit: "cup", Item: "butter"}
	if got, _ := Scaled(cup, 2); got != "2 cups butter" {
		t.Errorf("plural unit: got %q", got)
	}
	cups := recipe.Ingredient{Raw: "2 cups flour", ParseOK: true, QtyMin: 2, QtyMax: 2, Unit: "cups", Item: "flour"}
	if got, _ := Scaled(cups, 0.5); got != "1 cup flour" {
		t.Errorf("singular unit: got %q", got)
	}

	if got, _ := Scaled(rng, 1); got != rng.Raw {
		t.Errorf("factor 1 must keep the raw text: got %q", got)
	}
}
