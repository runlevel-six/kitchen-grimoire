// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/runlevel-six/kitchen-grimoire/internal/ingredient"
)

// Normalizer rewrites legacy "Noun--Qualifier" ingredient naming into
// natural order (§8.4 step 5): "Cheese--Cheddar" → "Cheddar cheese",
// "Flour--All Purpose" → "all-purpose flour". An override file maps whole
// names when the rules get one wrong. The zero value applies the rules
// only.
type Normalizer struct {
	overrides map[string]string // key: lower-cased name with "--" separators
}

// LoadNormalizer reads an override CSV with columns from,to, e.g.
// "Beans--Kidney,red kidney beans". A header row (first cell "from") and
// '#' comment lines are skipped.
func LoadNormalizer(r io.Reader) (*Normalizer, error) {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	n := &Normalizer{overrides: map[string]string{}}
	first := true
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("normalize file: %w", err)
		}
		if first {
			first = false
			if strings.EqualFold(strings.TrimSpace(rec[0]), "from") {
				continue
			}
		}
		if len(rec) < 2 || strings.TrimSpace(rec[0]) == "" || strings.TrimSpace(rec[1]) == "" {
			line, _ := cr.FieldPos(0)
			return nil, fmt.Errorf("normalize file line %d: want from,to", line)
		}
		n.overrides[nameKey(rec[0])] = strings.TrimSpace(rec[1])
	}
	return n, nil
}

// nameSep is a legacy naming separator: two hyphens or an em dash, with no
// space on either side (an em dash between spaces is punctuation).
var nameSep = regexp.MustCompile(`(\S)(?:--|—)(\S)`)

// nameKey lower-cases a name and writes every separator as "--".
func nameKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "—", "--")
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// properQualifiers keep their capital letter when moved in front of the
// noun ("Cheese--Cheddar" → "Cheddar cheese"). Everything else is
// lower-cased ("Sugar--Powdered" → "powdered sugar").
var properQualifiers = map[string]bool{
	"american": true, "cheddar": true, "colby": true, "dijon": true, "french": true,
	"greek": true, "italian": true, "mexican": true, "monterey jack": true,
	"parmesan": true, "romano": true, "swiss": true, "worcestershire": true,
	"cajun": true, "creole": true, "spanish": true, "german": true, "polish": true,
	"canadian": true, "english": true, "thai": true, "asiago": true, "gruyere": true,
	"gruyère": true, "provolone": true, "havarti": true, "gouda": true, "brie": true,
}

// qualifierSpelling fixes qualifiers that are hyphenated in natural order.
var qualifierSpelling = map[string]string{
	"all purpose":   "all-purpose",
	"self rising":   "self-rising",
	"semi sweet":    "semi-sweet",
	"semisweet":     "semisweet",
	"extra virgin":  "extra-virgin",
	"sun dried":     "sun-dried",
	"low fat":       "low-fat",
	"fat free":      "fat-free",
	"sugar free":    "sugar-free",
	"quick cooking": "quick-cooking",
}

// Line normalizes one ingredient line and reports whether it changed.
// Only the item is rewritten; the quantity, size, unit, and note keep
// their wording ("1 cup Cheese--Cheddar, shredded" → "1 cup Cheddar
// cheese, shredded").
func (n *Normalizer) Line(raw string) (string, bool) {
	if !nameSep.MatchString(raw) {
		return raw, false
	}
	// Find where the item starts: after the parsed quantity/size/unit.
	itemStart := 0
	if p := ingredient.Parse(raw); p.ParseOK {
		if i := strings.Index(raw, p.Item); i >= 0 {
			itemStart = i
		}
	} else {
		itemStart = len(raw) - len(strings.TrimLeft(raw, " \t"))
	}
	// The item ends at the note (", ") or a parenthesis.
	itemEnd := len(raw)
	if i := strings.Index(raw[itemStart:], ", "); i >= 0 {
		itemEnd = itemStart + i
	}
	if i := strings.IndexAny(raw[itemStart:itemEnd], "(,;"); i >= 0 {
		itemEnd = itemStart + i
	}
	item := strings.TrimRight(raw[itemStart:itemEnd], " ")
	if !nameSep.MatchString(item) {
		return raw, false
	}
	natural := n.name(item)
	if natural == item {
		return raw, false
	}
	return raw[:itemStart] + natural + raw[itemStart+len(item):], true
}

// name rewrites one "Noun--Qualifier[--Qualifier]" name.
func (n *Normalizer) name(item string) string {
	if n != nil {
		if to, ok := n.overrides[nameKey(item)]; ok {
			return to
		}
	}
	parts := strings.Split(strings.ReplaceAll(item, "—", "--"), "--")
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return item // "--" at an end: not a name
		}
	}
	// Natural order is the qualifiers, innermost last, then the noun:
	// "Beans--Kidney--Dark Red" → "dark red kidney beans".
	words := make([]string, 0, len(parts))
	for i := len(parts) - 1; i >= 1; i-- {
		words = append(words, qualifier(parts[i]))
	}
	words = append(words, strings.ToLower(strings.TrimSpace(parts[0])))
	return strings.Join(words, " ")
}

func qualifier(q string) string {
	q = strings.Join(strings.Fields(q), " ")
	low := strings.ToLower(q)
	if properQualifiers[low] {
		return q
	}
	if s, ok := qualifierSpelling[low]; ok {
		return s
	}
	return low
}
