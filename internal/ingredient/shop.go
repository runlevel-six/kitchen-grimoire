// SPDX-License-Identifier: Apache-2.0

package ingredient

import (
	"slices"
	"strings"
)

// ShopLine is one ingredient line going into a shopping list, with the
// factor its meal entry is scaled by and the recipe it came from.
type ShopLine struct {
	Line   Line
	Factor float64
	Recipe string
}

// ShopItem is one row of a shopping list: an item and every amount needed,
// one per unit and size ("1 cup + 3 Tbsp"). Amounts is empty when no line
// gave a quantity.
type ShopItem struct {
	Item    string
	Amounts []string
	Recipes []string
}

// ShopRaw is a line kept exactly as written, with its recipe.
type ShopRaw struct {
	Raw    string
	Recipe string
}

// ShoppingList is a meal's ingredients, scaled and combined (§9.4).
type ShoppingList struct {
	Items        []ShopItem // alphabetical by item
	Staples      []ShopItem // likely on hand: salt, pepper, water, ...
	StaplesRaw   []ShopRaw  // unparsed staple lines ("salt and pepper to taste")
	Unparsed     []ShopRaw  // everything the parser didn't understand, verbatim
	LineCount    int
	CombinedFrom int // parsed lines that went into Items and Staples
}

// staples are items most kitchens have. They're listed, just collapsed.
var staples = map[string]bool{
	"salt": true, "kosher salt": true, "sea salt": true, "table salt": true, "coarse salt": true,
	"pepper": true, "black pepper": true, "ground black pepper": true, "ground pepper": true,
	"freshly ground black pepper": true, "salt and pepper": true,
	"water": true, "cold water": true, "warm water": true, "hot water": true, "boiling water": true,
	"ice water": true, "ice": true, "ice cube": true,
	"cooking spray": true, "nonstick cooking spray": true, "non-stick cooking spray": true,
}

// stapleRawPrefixes catch unparsed staple lines such as "Salt to taste".
var stapleRawPrefixes = []string{
	"salt", "pepper", "kosher salt", "sea salt", "black pepper", "freshly ground", "water",
	"cooking spray", "nonstick cooking spray", "ice",
}

type amountKey struct{ unit, size string }

type amount struct {
	min, max float64
	unit     string // display unit (canonical spelling)
	size     string
}

type itemAcc struct {
	key      string
	singular string // as written in a line where it looked singular
	plural   string // as written in a line where it looked plural
	other    string // first spelling seen
	order    []amountKey
	amounts  map[amountKey]*amount
	recipes  []string
	qtyTotal float64 // for choosing singular or plural when there's no unit
}

// Shop combines lines into a shopping list. Parsed lines with the same
// item, unit, and size are added together; nothing is converted between
// units (OQ-08). Unparsed lines are never dropped: they're listed verbatim.
func Shop(lines []ShopLine) ShoppingList {
	var out ShoppingList
	acc := map[string]*itemAcc{}
	var keys []string
	for _, sl := range lines {
		in := sl.Line
		if strings.TrimSpace(in.Raw) == "" {
			continue
		}
		out.LineCount++
		if !in.ParseOK {
			raw := ShopRaw{Raw: in.Raw, Recipe: sl.Recipe}
			if isStapleRaw(in.Raw) {
				out.StaplesRaw = append(out.StaplesRaw, raw)
			} else {
				out.Unparsed = append(out.Unparsed, raw)
			}
			continue
		}
		out.CombinedFrom++
		f := sl.Factor
		if f <= 0 {
			f = 1
		}
		key := ItemKey(in.Item)
		a := acc[key]
		if a == nil {
			a = &itemAcc{key: key, other: in.Item, amounts: map[amountKey]*amount{}}
			acc[key] = a
			keys = append(keys, key)
		}
		if strings.EqualFold(strings.TrimSpace(in.Item), key) {
			if a.singular == "" {
				a.singular = in.Item
			}
		} else if a.plural == "" {
			a.plural = in.Item
		}
		if !slices.Contains(a.recipes, sl.Recipe) {
			a.recipes = append(a.recipes, sl.Recipe)
		}
		if in.QtyMin <= 0 {
			continue
		}
		unit := CanonicalUnit(in.Unit)
		ak := amountKey{unit: unit, size: strings.ToLower(strings.Join(strings.Fields(in.Size), " "))}
		am := a.amounts[ak]
		if am == nil {
			am = &amount{unit: unit, size: in.Size}
			a.amounts[ak] = am
			a.order = append(a.order, ak)
		}
		hi := in.QtyMax
		if hi < in.QtyMin {
			hi = in.QtyMin
		}
		am.min += in.QtyMin * f
		am.max += hi * f
		if unit == "" && ak.size == "" {
			a.qtyTotal += hi * f
		}
	}

	slices.Sort(keys)
	for _, k := range keys {
		a := acc[k]
		it := ShopItem{Item: a.displayName(), Recipes: a.recipes}
		for _, ak := range a.order {
			it.Amounts = append(it.Amounts, a.amounts[ak].String())
		}
		if staples[k] {
			out.Staples = append(out.Staples, it)
		} else {
			out.Items = append(out.Items, it)
		}
	}
	return out
}

// displayName picks the spelling to show: plural when a unit-less count is
// above one ("3 eggs"), otherwise singular, otherwise whatever was written.
func (a *itemAcc) displayName() string {
	if a.qtyTotal > 1 && a.plural != "" {
		return a.plural
	}
	if a.singular != "" {
		return a.singular
	}
	if a.qtyTotal > 0 && a.qtyTotal <= 1 {
		return singularPhrase(strings.Join(strings.Fields(a.other), " "))
	}
	return a.other
}

func (am *amount) String() string {
	qty := FormatQty(am.min)
	if FormatQty(am.max) != qty {
		qty += "–" + FormatQty(am.max)
	}
	parts := []string{qty}
	if am.size != "" {
		parts = append(parts, am.size)
	}
	if am.unit != "" {
		parts = append(parts, UnitFor(unitDisplay(am.unit), am.max))
	}
	return strings.Join(parts, " ")
}

// unitDisplay is how a canonical unit is written on the list.
func unitDisplay(canon string) string {
	switch canon {
	case "tbsp":
		return "Tbsp"
	case "l":
		return "L"
	}
	return canon
}

// ItemKey normalizes an item for matching: lower case, single spaces, and
// a light singular form ("Eggs" and "egg" match; "berries" → "berry").
// Different wording ("unsalted butter" vs "butter") deliberately does not
// match: guessing would hide a real difference.
func ItemKey(item string) string {
	s := strings.ToLower(strings.Join(strings.Fields(item), " "))
	if s == "" {
		return s
	}
	head, last := "", s
	if i := strings.LastIndexByte(s, ' '); i >= 0 {
		head, last = s[:i+1], s[i+1:]
	}
	return head + singular(last)
}

func singular(w string) string {
	switch {
	case len(w) <= 3:
		return w
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "oes"),
		strings.HasSuffix(w, "ches"), strings.HasSuffix(w, "shes"),
		strings.HasSuffix(w, "xes"), strings.HasSuffix(w, "sses"):
		return w[:len(w)-2]
	case strings.HasSuffix(w, "ss"), strings.HasSuffix(w, "us"), strings.HasSuffix(w, "is"):
		return w
	case strings.HasSuffix(w, "s"):
		return w[:len(w)-1]
	}
	return w
}

// singularPhrase makes the last word singular, keeping the case of the
// letters it keeps ("Eggs" → "Egg", "Berries" → "Berry").
func singularPhrase(s string) string {
	i := strings.LastIndexByte(s, ' ') + 1
	last := s[i:]
	low := strings.ToLower(last)
	one := singular(low)
	if one == low || len(low) != len(last) {
		return s
	}
	n := 0
	for n < len(one) && one[n] == low[n] {
		n++
	}
	return s[:i] + last[:n] + one[n:]
}

func isStapleRaw(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	for _, lead := range []string{"a pinch of ", "pinch of ", "a dash of ", "dash of ", "a few grinds of "} {
		s = strings.TrimPrefix(s, lead)
	}
	for _, p := range stapleRawPrefixes {
		if s == p || strings.HasPrefix(s, p+" ") || strings.HasPrefix(s, p+",") {
			return true
		}
	}
	return false
}
