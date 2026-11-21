// SPDX-License-Identifier: Apache-2.0

// Package ingredient parses, scales, and formats ingredient lines
// (docs/DESIGN.md §9). This file covers formatting and scaling; the line
// parser arrives in P1.3.
package ingredient

import (
	"math"
	"strconv"
	"strings"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// eighths maps a count of eighths (1..7) to its unicode fraction.
var eighths = [8]string{"", "⅛", "¼", "⅜", "½", "⅝", "¾", "⅞"}

// thirds are common enough in recipes to keep exact rather than rounding
// 1/3 to 3/8.
const third = 1.0 / 3

// FormatQty renders a quantity the way a cook reads it: nearest 1/8 (or
// 1/3, 2/3) as a unicode fraction, e.g. 1.5 → "1½", 0.333 → "⅓".
func FormatQty(q float64) string {
	if q <= 0 {
		return "0"
	}
	whole := math.Floor(q)
	frac := q - whole

	// Prefer thirds when the fraction is closer to a third than to any eighth.
	for _, t := range []struct {
		v float64
		s string
	}{{third, "⅓"}, {2 * third, "⅔"}} {
		if math.Abs(frac-t.v) < 0.02 {
			return joinQty(whole, t.s)
		}
	}

	n := int(math.Round(frac * 8))
	if n == 8 {
		whole++
		n = 0
	}
	if whole == 0 && n == 0 {
		return "⅛" // never round a real amount down to nothing
	}
	return joinQty(whole, eighths[n])
}

func joinQty(whole float64, frac string) string {
	if whole == 0 {
		return frac
	}
	return strconv.FormatFloat(whole, 'f', 0, 64) + frac
}

// unitForms maps every spelled-out unit to its singular and plural. Abbreviations
// (tsp, Tbsp, oz, lb, pkg) are the same either way and are not listed.
var unitForms = func() map[string][2]string {
	pairs := [][2]string{
		{"cup", "cups"}, {"can", "cans"}, {"clove", "cloves"}, {"package", "packages"},
		{"envelope", "envelopes"}, {"stick", "sticks"}, {"pinch", "pinches"}, {"dash", "dashes"},
		{"pint", "pints"}, {"quart", "quarts"}, {"gallon", "gallons"}, {"bunch", "bunches"},
		{"jar", "jars"}, {"box", "boxes"}, {"bag", "bags"}, {"slice", "slices"}, {"sprig", "sprigs"},
		{"teaspoon", "teaspoons"}, {"tablespoon", "tablespoons"}, {"ounce", "ounces"}, {"pound", "pounds"},
	}
	m := map[string][2]string{}
	for _, p := range pairs {
		m[p[0]], m[p[1]] = p, p
	}
	return m
}()

// UnitFor returns unit in the singular for quantities up to 1 and the plural
// above it ("1 cup", "1½ cups", "½ cup"). Unknown units are returned as is.
func UnitFor(unit string, qty float64) string {
	forms, ok := unitForms[strings.ToLower(unit)]
	if !ok {
		return unit
	}
	if qty > 1 {
		return forms[1]
	}
	return forms[0]
}

// Scaled renders an ingredient line multiplied by factor. Lines that did
// not parse are returned verbatim with scaled=false so the UI can mark them
// "not scaled" (FR-08). Size text ("8-oz") is never scaled (§9.2).
func Scaled(in recipe.Ingredient, factor float64) (line string, scaled bool) {
	if !in.ParseOK || factor == 1 {
		return in.Raw, in.ParseOK
	}
	qty := FormatQty(in.QtyMin * factor)
	if in.QtyMax != in.QtyMin {
		qty += "–" + FormatQty(in.QtyMax*factor)
	}
	parts := []string{qty}
	unit := UnitFor(in.Unit, in.QtyMax*factor)
	for _, p := range []string{in.Size, unit, in.Item} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	line = strings.Join(parts, " ")
	if in.Note != "" {
		line += ", " + in.Note
	}
	return line, true
}
