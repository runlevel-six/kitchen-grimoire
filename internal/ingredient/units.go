// SPDX-License-Identifier: Apache-2.0

package ingredient

import "strings"

// unitAliases maps every accepted spelling of a unit, lower-cased, to its
// canonical key. "t" (teaspoon) and "T" (tablespoon) are deliberately
// missing: they differ only by case, so CanonicalUnit handles them first.
var unitAliases = func() map[string]string {
	groups := map[string][]string{
		"tsp":      {"tsp", "tsps", "teaspoon", "teaspoons"},
		"tbsp":     {"tbsp", "tbsps", "tbs", "tbl", "tbls", "tablespoon", "tablespoons"},
		"cup":      {"cup", "cups", "c"},
		"oz":       {"oz", "ozs", "ounce", "ounces"},
		"lb":       {"lb", "lbs", "pound", "pounds"},
		"pkg":      {"pkg", "pkgs", "package", "packages"},
		"can":      {"can", "cans"},
		"envelope": {"envelope", "envelopes"},
		"stick":    {"stick", "sticks"},
		"clove":    {"clove", "cloves"},
		"pinch":    {"pinch", "pinches"},
		"dash":     {"dash", "dashes"},
		"pint":     {"pint", "pints", "pt", "pts"},
		"quart":    {"quart", "quarts", "qt", "qts"},
		"gallon":   {"gallon", "gallons", "gal", "gals"},
		"bunch":    {"bunch", "bunches"},
		"jar":      {"jar", "jars"},
		"box":      {"box", "boxes"},
		"bag":      {"bag", "bags"},
		"slice":    {"slice", "slices"},
		"sprig":    {"sprig", "sprigs"},
		"g":        {"g", "gram", "grams"},
		"kg":       {"kg", "kgs", "kilogram", "kilograms"},
		"ml":       {"ml", "milliliter", "milliliters", "millilitre", "millilitres"},
		"l":        {"l", "liter", "liters", "litre", "litres"},
	}
	m := map[string]string{}
	for canon, spellings := range groups {
		for _, s := range spellings {
			m[s] = canon
		}
	}
	return m
}()

// sizeUnits are the units that can describe a package size ("8-oz").
var sizeUnits = map[string]bool{"oz": true, "lb": true, "g": true, "kg": true, "ml": true, "l": true}

// containerUnits are the units that a space-separated size ("2 8 oz cans")
// may come before.
var containerUnits = map[string]bool{
	"pkg": true, "can": true, "jar": true, "box": true, "bag": true, "envelope": true, "stick": true,
}

// CanonicalUnit returns the canonical key for a unit as written in a recipe
// ("Tbsp." → "tbsp", "cups" → "cup", "T" → "tbsp", "t" → "tsp"), or "" if
// unit is not a known unit. One trailing period is ignored. Matching is
// case-insensitive except for "t" and "T".
func CanonicalUnit(unit string) string {
	unit = strings.TrimSuffix(unit, ".")
	switch unit {
	case "t":
		return "tsp"
	case "T":
		return "tbsp"
	}
	return unitAliases[strings.ToLower(unit)]
}
