// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/runlevel-six/kitchen-grimoire/internal/ingredient"
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// boundaryLen is the line length (in characters) above which a line is
// taken to be instruction prose rather than an ingredient (§8.4 step 3).
const boundaryLen = 75

// Review reasons the body parser can raise.
const (
	ReasonNoIngredients    = "no_ingredients"
	ReasonNoSteps          = "no_steps"
	ReasonMultiColumn      = "multi_column_ingredients"
	ReasonGroupAmbiguous   = "group_label_ambiguous"
	ReasonTitleUncertain   = "title_parse_uncertain"
	ReasonPossibleTitle    = "possible_missed_title"
	ReasonOlderOnly        = "only_in_older_edition"
	ReasonTitleChanged     = "title_changed_between_editions"
	reasonUnclassifiedPref = "unclassified_token:"
	reasonUnsplitPref      = "unsplit_fields:"
)

// body is the parsed content of a block.
type body struct {
	groups  []recipe.IngredientGroup
	steps   []string
	reasons []string
}

// sentenceEnd matches sentence punctuation at the end of a line, possibly
// followed by closing quotes or parentheses. Group 1 is the word before it.
var sentenceEnd = regexp.MustCompile(`(\S*?)[.!?]["'”’)\]]*$`)

// innerSentence matches a sentence ending inside a line ("…smooth. Shape").
var innerSentence = regexp.MustCompile(`(\S+)[.!?]["'”’)]*\s+[A-Z]`)

// abbreviations end in a period without ending a sentence ("1 pkg. (8 oz.)").
var abbreviations = map[string]bool{
	"oz": true, "lb": true, "lbs": true, "tsp": true, "tbsp": true, "tbs": true, "pkg": true,
	"pkgs": true, "c": true, "t": true, "qt": true, "pt": true, "doz": true, "approx": true,
	"env": true, "sq": true, "in": true, "lg": true, "med": true, "sm": true, "gal": true,
	"cont": true, "ctn": true, "pk": true, "fl": true, "no": true, "st": true, "mr": true,
	"mrs": true, "dr": true, "jr": true, "sr": true,
}

// isAbbrev reports whether a word (with leading punctuation and a digit
// prefix such as "8oz" removed) is a known abbreviation.
func isAbbrev(word string) bool {
	w := strings.TrimLeftFunc(word, func(r rune) bool { return !unicode.IsLetter(r) })
	return abbreviations[strings.ToLower(w)]
}

// hasSentenceEnd reports whether line ends with sentence punctuation that
// is not just an abbreviation's period.
func hasSentenceEnd(line string) bool {
	line = strings.TrimRight(line, " \t")
	m := sentenceEnd.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	trimmed := strings.TrimRight(line, "\"'”’)]")
	if strings.HasSuffix(trimmed, ".") && isAbbrev(m[1]) {
		return false
	}
	return true
}

// isProse reports whether a line reads as instruction text, which ends
// the ingredient list: it is long, ends a sentence, or contains a
// sentence ending followed by another sentence.
func isProse(line string) bool {
	t := collapse(line)
	if utf8.RuneCountInString(t) > boundaryLen || hasSentenceEnd(t) {
		return true
	}
	for _, m := range innerSentence.FindAllStringSubmatch(t, -1) {
		if !isAbbrev(m[1]) {
			return true
		}
	}
	return false
}

// wrapMinLen is the shortest line taken as wrapped prose by wrapsIntoNext.
const wrapMinLen = 40

// wrapsIntoNext catches the first line of a wrapped PDF paragraph that is
// short enough to pass as an ingredient: a fairly long line, not starting
// with a quantity, whose next line starts in lower case (a sentence
// carrying on).
func wrapsIntoNext(texts []string, i int) bool {
	t := collapse(texts[i])
	if utf8.RuneCountInString(t) < wrapMinLen || qtyStart.MatchString(t) || i+1 >= len(texts) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(collapse(texts[i+1]))
	return unicode.IsLower(r)
}

// collapse replaces tabs with spaces and trims.
func collapse(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// knownLabels are the last words of ingredient group labels.
var knownLabels = map[string]bool{
	"filling": true, "frosting": true, "topping": true, "icing": true, "sauce": true,
	"crust": true, "pastry": true, "glaze": true, "dough": true, "batter": true,
	"dressing": true, "marinade": true, "streusel": true, "meringue": true, "syrup": true,
	"garnish": true, "base": true, "cake": true, "layer": true, "crumbs": true,
	"gravy": true, "seasoning": true, "rub": true, "dip": true,
}

// labelLine matches a short line that is only a label: "Filling:",
// "For the frosting:", "CREAM CHEESE ICING:".
var labelLine = regexp.MustCompile(`^([\p{L}][\p{L}'’&\- ]{0,40}):$`)

// inlineLabel matches a label followed by text on the same line.
var inlineLabel = regexp.MustCompile(`^([\p{L}][\p{L}'’&\- ]{0,40}):\s+\S`)

// labelName returns the group name for a label line and whether the label
// is a known kind: its last word is a known label, the map lists it as a
// group_label, or it is an ALL-CAPS sub-recipe heading.
func labelName(label string, m *Map) (string, bool) {
	label = strings.TrimSpace(label)
	if e := m.Lookup(label); e != nil && e.Class == ClassGroupLabel {
		return e.Canonical, true
	}
	name := label
	low := strings.ToLower(name)
	for _, p := range []string{"for the ", "for "} {
		if strings.HasPrefix(low, p) {
			name = strings.TrimSpace(name[len(p):])
			low = strings.ToLower(name)
			break
		}
	}
	if name == "" || len(strings.Fields(name)) > 4 {
		return "", false
	}
	fields := strings.Fields(low)
	if knownLabels[fields[len(fields)-1]] {
		return upperFirst(name), true
	}
	if !hasLower(name) && upperCount(name) >= 2 {
		return name, true // "SPECIAL SAUCE:" sub-recipe heading
	}
	return name, false
}

func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// multiHeader matches a column header for a multi-batch table:
// "For 3 dozen:<tab>for 1 dozen:" or the same with spaces.
var multiHeader = regexp.MustCompile(`(?i)^for\b[^:\t]*:[\t ]+for\b[^:]*:?$`)

// qtyStart matches a quantity at the start of a field.
var qtyStart = regexp.MustCompile(`^(?:\d|[½⅓⅔¼¾⅛⅜⅝⅞⅕⅙])`)

// trailingQty matches a second quantity at the end of a PDF line:
// "1 cup corn meal 1/3 cup" → "1/3", "cup".
var trailingQty = regexp.MustCompile(`\s(\d+(?:\s+\d+/\d+|/\d+|\.\d+)?|\d*[½⅓⅔¼¾⅛⅜⅝⅞⅕⅙])(?:\s+([A-Za-z]+\.?))?$`)

// isMultiColumn reports whether an ingredient line belongs to a table with
// two or more quantity columns.
func isMultiColumn(line string, format Format) bool {
	t := strings.TrimSpace(line)
	if multiHeader.MatchString(collapse(t)) {
		return true
	}
	if strings.Contains(t, "\t") {
		n, headers := 0, 0
		for f := range strings.SplitSeq(t, "\t") {
			f = strings.TrimSpace(f)
			if qtyStart.MatchString(f) {
				n++
			}
			if strings.HasSuffix(f, ":") {
				headers++
			}
		}
		if n >= 2 || headers >= 2 {
			return true
		}
	}
	if format != FormatPDFText || !qtyStart.MatchString(t) {
		return false
	}
	m := trailingQty.FindStringSubmatchIndex(t)
	if m == nil || m[0] == 0 {
		return false
	}
	head := t[:m[0]]
	if !strings.ContainsFunc(head, unicode.IsLetter) {
		return false // "1 1/2" is one quantity
	}
	if m[4] < 0 {
		return true // a bare number after the item
	}
	unit := t[m[4]:m[5]]
	if ingredient.CanonicalUnit(unit) != "" {
		return true
	}
	// "3 eggs 1 egg": the trailing word repeats an earlier one.
	stem := strings.TrimSuffix(strings.ToLower(unit), "s")
	return len(stem) >= 3 && strings.Contains(strings.ToLower(head), stem)
}

// parseBody separates a block's lines into ingredients and steps (§8.4
// step 3) and applies the group and table rules.
func parseBody(lines []srcLine, format Format, m *Map, norm *Normalizer) body {
	var b body
	var texts []string
	for _, l := range lines {
		if !l.marker {
			texts = append(texts, l.text)
		}
	}
	flag := func(reason string) {
		for _, r := range b.reasons {
			if r == reason {
				return
			}
		}
		b.reasons = append(b.reasons, reason)
	}

	// 1. Find the boundary: the first prose line.
	boundary := len(texts)
	for i, t := range texts {
		if strings.TrimSpace(t) != "" && (isProse(t) || format == FormatPDFText && wrapsIntoNext(texts, i)) {
			boundary = i
			break
		}
	}

	// A label line with no ingredient after it (before the boundary)
	// belongs to the instructions, ambiguously.
	for i := boundary - 1; i >= 0; i-- {
		t := collapse(texts[i])
		if t == "" {
			continue
		}
		if lm := labelLine.FindStringSubmatch(t); lm != nil && !multiHeader.MatchString(t) {
			boundary = i
			flag(ReasonGroupAmbiguous)
			continue
		}
		break
	}

	// 2. Ingredients, split into groups at label lines.
	cur := recipe.IngredientGroup{}
	flush := func() {
		if len(cur.Ingredients) > 0 {
			b.groups = append(b.groups, cur)
		}
		cur = recipe.IngredientGroup{}
	}
	for _, raw := range texts[:boundary] {
		t := collapse(raw)
		if t == "" {
			continue
		}
		if isMultiColumn(raw, format) {
			flag(ReasonMultiColumn)
			// Kept as written (tabs included) and never parsed.
			cur.Ingredients = append(cur.Ingredients, recipe.Ingredient{Raw: strings.TrimSpace(raw)})
			continue
		}
		if lm := labelLine.FindStringSubmatch(t); lm != nil {
			if name, known := labelName(lm[1], m); known {
				flush()
				cur.Name = name
				continue
			}
			flag(ReasonGroupAmbiguous)
		} else if lm := inlineLabel.FindStringSubmatch(t); lm != nil {
			if _, known := labelName(lm[1], m); known {
				flag(ReasonGroupAmbiguous) // "Topping: 1 cup crumbs": kept as one line
			}
		}
		line, _ := norm.Line(t)
		cur.Ingredients = append(cur.Ingredients, ingredient.Parse(line))
	}
	flush()

	// 3. Instructions: one paragraph per step.
	rest := texts[boundary:]
	b.steps = paragraphs(rest, format)
	for i, raw := range rest {
		t := collapse(raw)
		lm := labelLine.FindStringSubmatch(t)
		if lm == nil {
			continue
		}
		// A bare label inside the instructions followed by quantity lines
		// looks like a second ingredient list: say so, but leave the text.
		for _, next := range rest[i+1:] {
			if n := collapse(next); n != "" {
				if qtyStart.MatchString(n) {
					flag(ReasonGroupAmbiguous)
				}
				break
			}
		}
	}
	return b
}

// paragraphs turns instruction lines into steps. Word-processor text has
// one paragraph per line. PDF text is wrapped, so a line that doesn't end
// a sentence continues into the next. Blank lines always separate. In both
// flavours a line ending in ':' ("SPECIAL SAUCE:") runs into the next, so
// a sub-heading stays with the text it introduces.
func paragraphs(lines []string, format Format) []string {
	var out []string
	var cur strings.Builder
	end := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, raw := range lines {
		t := collapse(raw)
		if t == "" {
			end()
			continue
		}
		if cur.Len() > 0 {
			s := cur.String()
			switch {
			case format != FormatPDFText && !strings.HasSuffix(s, ":"):
				end()
			case strings.HasSuffix(s, "-") && len(s) > 1 && s[len(s)-2] != ' ':
				// A hyphen at a wrap joins without a space ("well-" "beaten").
			default:
				cur.WriteByte(' ')
			}
		}
		cur.WriteString(t)
		if format == FormatPDFText && hasSentenceEnd(t) {
			end()
		}
	}
	end()
	return out
}
