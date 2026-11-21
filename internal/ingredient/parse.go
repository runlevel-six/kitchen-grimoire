// SPDX-License-Identifier: Apache-2.0

package ingredient

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxQty is the largest quantity we believe. Anything above it is almost
// certainly a misread (a year, a phone number, a zip code), so the line is
// kept as free text instead.
const maxQty = 10000

// unicodeFractions maps the single-character fractions we accept to their
// values. They may stand alone ("½ cup") or follow an integer ("1½ cups").
var unicodeFractions = map[rune]float64{
	'½': 1.0 / 2, '⅓': 1.0 / 3, '⅔': 2.0 / 3, '¼': 1.0 / 4, '¾': 3.0 / 4,
	'⅛': 1.0 / 8, '⅜': 3.0 / 8, '⅝': 5.0 / 8, '⅞': 7.0 / 8,
	'⅕': 1.0 / 5, '⅖': 2.0 / 5, '⅗': 3.0 / 5, '⅘': 4.0 / 5, '⅙': 1.0 / 6, '⅚': 5.0 / 6,
}

// numPattern matches one number in any of the forms the grammar allows
// (§9.1). Go's regexp tries the alternatives left to right and keeps the
// first one that fits, so the order matters: the mixed number "1 1/2" must
// be tried before the plain integer "1", and "1/2" before "1".
const numPattern = `(?:` +
	`\d+\s+\d+/\d+` + // mixed: 1 1/2
	`|\d+\s*[½⅓⅔¼¾⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚]` + // integer + unicode fraction: 1½, 1 ½
	`|\d+/\d+` + // fraction: 1/2
	`|\d+(?:\.\d+)?` + // integer or decimal: 2, 1.5
	`|\.\d+` + // bare decimal: .5
	`|[½⅓⅔¼¾⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚]` + // unicode fraction alone: ½
	`)`

// qtyRe matches the quantity at the start of a line: a number, optionally
// followed by a range ("3-4", "3–4", "3 to 4").
var qtyRe = regexp.MustCompile(`^(` + numPattern + `)(?:\s*[-–]\s*(` + numPattern + `)|\s+to\s+(` + numPattern + `))?`)

// sizeRe matches a package size written as a number and a weight/volume
// unit: "8-oz", "8 oz", "14.5-ounce". Group 2 is the separator, group 3 the
// unit word (checked against sizeUnits by the caller).
var sizeRe = regexp.MustCompile(`^(` + numPattern + `)(-|\s+)([A-Za-z]+\.?)`)

// Parse splits one ingredient line into quantity, size, unit, item and note
// following the grammar in docs/DESIGN.md §9.1:
//
//	line := [qty] [size] [unit] item [", " note] | free text
//
// A line that does not start with a sensible quantity is free text: only
// Raw is set and ParseOK is false. Parse never guesses and never panics.
func Parse(raw string) Line {
	raw = strings.TrimSpace(raw)
	free := Line{Raw: raw}

	// 1. Quantity.
	m := qtyRe.FindStringSubmatch(raw)
	if m == nil {
		return free
	}
	qtyMin, ok := numValue(m[1])
	if !ok {
		return free
	}
	qtyMax := qtyMin
	if hi := m[2] + m[3]; hi != "" { // at most one of the two range forms matched
		if qtyMax, ok = numValue(hi); !ok {
			return free
		}
	}
	if !(qtyMin > 0 && qtyMin <= qtyMax && qtyMax <= maxQty) {
		return free
	}
	rest := raw[len(m[0]):]

	// The quantity must end at a word boundary. The one exception is a unit
	// glued to the number ("500g", "1½cups"); anything else ("2-inch",
	// "3eggs", "1.") is free text.
	if rest != "" && !startsWithSpace(rest) {
		if w, _ := nextWord(rest); CanonicalUnit(w) == "" {
			return free
		}
	}
	rest = strings.TrimSpace(rest)

	// 2. Size, then 3. unit.
	size, rest := parseSize(rest)
	unit := ""
	if w, after := nextWord(rest); CanonicalUnit(w) != "" {
		unit, rest = w, strings.TrimSpace(after)
		// "1 cup of flour" → item "flour".
		if len(rest) > 3 && strings.EqualFold(rest[:3], "of ") {
			rest = strings.TrimSpace(rest[3:])
		}
	}

	// 4. Item and note, split at the first ", ".
	item, note, _ := strings.Cut(rest, ", ")
	item = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(item), ","))
	if item == "" {
		return free // e.g. "2 cups": a quantity of nothing
	}
	if endsWithQuantity(item) {
		// "1 cup corn meal 1/3 cup" is a two-column table (one quantity per
		// batch size), not one amount: scaling it would be wrong, so keep
		// it as written.
		return free
	}
	return Line{
		Raw:     raw,
		ParseOK: true,
		QtyMin:  qtyMin,
		QtyMax:  qtyMax,
		Size:    size,
		Unit:    unit,
		Item:    item,
		Note:    strings.TrimSpace(note),
	}
}

// parseSize looks for a package size at the start of s and returns it as
// written plus the remaining text. Two forms are recognised:
//
//   - parenthesized, containing a number: "(15 oz)", "(15-ounce)";
//   - number and weight unit, either hyphenated ("8-oz", any following
//     word) or space-separated ("8 oz", only when a container unit such as
//     "pkg" or "cans" follows, so "2 8 oz cans" works but we don't read too
//     much into other lines).
//
// If there is no size, it returns "" and s unchanged.
func parseSize(s string) (size, rest string) {
	if strings.HasPrefix(s, "(") {
		end := strings.IndexByte(s, ')')
		if end < 0 || !strings.ContainsAny(s[:end], "0123456789½⅓⅔¼¾⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚") {
			return "", s
		}
		after := s[end+1:]
		if after != "" && !startsWithSpace(after) {
			return "", s
		}
		return s[:end+1], strings.TrimSpace(after)
	}

	m := sizeRe.FindStringSubmatch(s)
	if m == nil || !sizeUnits[CanonicalUnit(m[3])] {
		return "", s
	}
	if _, ok := numValue(m[1]); !ok {
		return "", s
	}
	after := s[len(m[0]):]
	if !startsWithSpace(after) {
		return "", s // the size must be followed by something
	}
	after = strings.TrimSpace(after)
	if m[2] != "-" {
		// Space-separated form: only trust it before a container unit.
		if w, _ := nextWord(after); !containerUnits[CanonicalUnit(w)] {
			return "", s
		}
	}
	return m[0], after
}

// numValue converts one number matched by numPattern to its value. It
// reports false for nonsense such as a zero denominator or an overflow.
func numValue(s string) (float64, bool) {
	s = strings.TrimSpace(s)

	// Unicode fraction, alone or after an integer: "½", "1½", "1 ½".
	if r, n := utf8.DecodeLastRuneInString(s); unicodeFractions[r] != 0 {
		whole := 0.0
		if w := strings.TrimSpace(s[:len(s)-n]); w != "" {
			var ok bool
			if whole, ok = parseFloat(w); !ok {
				return 0, false
			}
		}
		return whole + unicodeFractions[r], true
	}

	// Mixed number: "1 1/2".
	if f := strings.Fields(s); len(f) == 2 {
		whole, ok1 := parseFloat(f[0])
		frac, ok2 := parseFraction(f[1])
		return whole + frac, ok1 && ok2
	}

	if strings.Contains(s, "/") {
		return parseFraction(s)
	}
	return parseFloat(s)
}

// parseFraction converts "a/b" to a/b, refusing a zero denominator.
func parseFraction(s string) (float64, bool) {
	a, b, found := strings.Cut(s, "/")
	if !found {
		return 0, false
	}
	num, ok1 := parseFloat(a)
	den, ok2 := parseFloat(b)
	if !ok1 || !ok2 || den == 0 {
		return 0, false
	}
	return num / den, true
}

// parseFloat is strconv.ParseFloat that also rejects infinities.
func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

// nextWord returns the text up to the first space or comma, and what
// follows it.
func nextWord(s string) (word, rest string) {
	if i := strings.IndexAny(s, " \t,"); i >= 0 {
		return s[:i], s[i:]
	}
	return s, ""
}

func startsWithSpace(s string) bool {
	return s != "" && (s[0] == ' ' || s[0] == '\t')
}

// trailingQtyRe matches a quantity, optionally followed by one word, at the
// end of an item: "… 1/3 cup", "… 1 egg", "… 2".
var trailingQtyRe = regexp.MustCompile(`\s(?:\d+\s+\d+/\d+|\d+/\d+|\d+(?:\.\d+)?|\d*[½⅓⅔¼¾⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚])(?:\s+[A-Za-z]+\.?)?$`)

var parenRe = regexp.MustCompile(`\([^()]*\)`)

// endsWithQuantity reports whether an item ends with a second quantity,
// ignoring parentheticals ("sugar (or 3/4 cup honey)" is fine).
func endsWithQuantity(item string) bool {
	return trailingQtyRe.MatchString(strings.TrimSpace(parenRe.ReplaceAllString(item, "")))
}
