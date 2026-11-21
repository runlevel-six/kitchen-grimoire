// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxTitleLine bounds the lines considered as title lines. Real title
// lines are short; the bound keeps title detection linear on hostile input.
const maxTitleLine = 300

// Block is one recipe's slice of the source: its title line and every
// line up to the next title line.
type Block struct {
	Ordinal int    // 1-based position in the source
	Raw     string // the exact source bytes, page markers included
	Title   string // as written, e.g. "CHEESE BALL"
	// Fields is the title line after the title: the category,
	// subcategory, and attribution text, tabs kept.
	Fields string
	// TitleUncertain is set when the title had to be pried from its
	// fields by a guess (glued to the category, or a mixed-case suffix).
	TitleUncertain bool
	head           srcLine   // the title line
	lines          []srcLine // body lines, after the title line
}

// Document is a split source.
type Document struct {
	Format Format
	// Prefix is the text before the first title line (a cover page or an
	// index), ignored by the converter but kept for accounting.
	Prefix      string
	PrefixLines int // non-blank, non-marker lines in Prefix
	Blocks      []Block
}

// Split cuts text into recipe blocks at title lines (§8.4 step 2). The
// prefix and the blocks' Raw fields concatenate back to text exactly.
func Split(text string, format Format, m *Map) *Document {
	return split(text, format, m, false)
}

// split is Split, optionally with the loose title rules (see parseTitle).
func split(text string, format Format, m *Map, loose bool) *Document {
	if format == FormatAuto {
		format = DetectFormat(text)
	}
	d := &Document{Format: format}
	lines := scanLines(text)
	cur := -1 // index into d.Blocks of the open block
	prefixEnd := len(text)
	for _, l := range lines {
		if !l.marker {
			if t, ok := parseTitle(l.text, m, loose); ok {
				if cur < 0 {
					prefixEnd = l.start
				}
				d.Blocks = append(d.Blocks, Block{
					Ordinal:        len(d.Blocks) + 1,
					Title:          t.title,
					Fields:         t.fields,
					TitleUncertain: t.uncertain,
					head:           l,
				})
				cur = len(d.Blocks) - 1
				continue
			}
		}
		if cur < 0 {
			if !l.marker && strings.TrimSpace(l.text) != "" {
				d.PrefixLines++
			}
			continue
		}
		d.Blocks[cur].lines = append(d.Blocks[cur].lines, l)
	}
	d.Prefix = text[:prefixEnd]
	// Each block's Raw runs from its title line to the next block's.
	for i := range d.Blocks {
		end := len(text)
		if i+1 < len(d.Blocks) {
			end = d.Blocks[i+1].head.start
		}
		d.Blocks[i].Raw = text[d.Blocks[i].head.start:end]
	}
	return d
}

// titleParse is a recognized title line.
type titleParse struct {
	title     string
	fields    string
	uncertain bool
}

// isTitleRune reports whether r may appear in an ALL-CAPS title (besides
// upper-case letters and digits).
func isTitleRune(r rune) bool {
	if unicode.IsUpper(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case ' ', '&', '\'', '’', '-', '—', '–', '.', ',', '/', '(', ')', '!', '"', '#', '“', '”':
		return true
	}
	return false
}

// parseTitle decides whether line is a title line: an ALL-CAPS title
// followed by a known category token. With loose set (used to discover
// tokens before the map is filled in, and to flag possibly missed titles)
// it also accepts a caps title followed by a tab or a Capitalized word.
//
// Rules, in order:
//   - a line ending in ':' is never a title (inline sub-recipes such as
//     "SPECIAL SAUCE:"), nor is one ending a sentence ("NOTE Salads are
//     best fresh.");
//   - the title is the longest caps prefix that ends at a word boundary
//     and is followed by a category token, so "CHEESE BALL Appetizers"
//     splits before "Appetizers" even though its "A" is upper-case;
//   - a title glued to its category ("LAYERED SALADSalads") is split
//     before the upper-case letter that starts the category word, and
//     flagged uncertain;
//   - a caps run ending in a dash or "(" may carry a short mixed-case
//     suffix ("FRIED EGGS—easiest way Breakfast Dishes"): the title runs
//     up to the first category token. Uncertain unless a tab ends it.
//
// A category matched in ALL CAPS doesn't count (unless the map spells it
// that way): "CAKES AND BREADS" is a heading, not "CAKES AND" + "Breads".
func parseTitle(line string, m *Map, loose bool) (titleParse, bool) {
	t := strings.TrimRight(strings.TrimLeft(line, " "), " \t")
	if t == "" || len(t) > maxTitleLine || strings.HasSuffix(t, ":") || hasSentenceEnd(t) {
		return titleParse{}, false
	}
	// e is the end of the caps run.
	e := 0
	for e < len(t) {
		r, n := utf8.DecodeRuneInString(t[e:])
		if !isTitleRune(r) {
			break
		}
		e += n
	}
	if upperCount(t[:e]) < 2 {
		return titleParse{}, false
	}

	if m.hasCategories() {
		// Candidates from the longest title down.
		for p := e; p > 0; p-- {
			if p < len(t) && !utf8.RuneStart(t[p]) {
				continue
			}
			if tp, ok := tryTitleSplit(t, p, m); ok {
				return tp, true
			}
		}
		if tp, ok := dashSuffixTitle(t, e, m); ok {
			return tp, true
		}
	}
	if loose {
		return looseTitle(t, e)
	}
	return titleParse{}, false
}

// tryTitleSplit tests the title/fields split at byte p of t.
func tryTitleSplit(t string, p int, m *Map) (titleParse, bool) {
	if p >= len(t) {
		return titleParse{}, false // nothing after the title
	}
	title := strings.TrimRight(t[:p], " ")
	rest := t[p:]
	if upperCount(title) < 2 {
		return titleParse{}, false
	}
	glued := !isSpaceOrTab(t[p-1]) && !isSpaceOrTab(t[p])
	if glued {
		// Only between an upper-case letter ending the title and a
		// Capitalized category word: "SALADS|alads" is not a split, nor is
		// "SALA|DSalads".
		last, _ := utf8.DecodeLastRuneInString(t[:p])
		if !unicode.IsUpper(last) || !startsCapitalizedWord(rest) {
			return titleParse{}, false
		}
	}
	if !startsWithCategory(rest, m) {
		return titleParse{}, false
	}
	return titleParse{title: title, fields: strings.TrimLeft(rest, " \t"), uncertain: glued}, true
}

// dashSuffixTitle handles a caps run ending in a dash or "(" followed by
// mixed-case words: the title runs to the first category token within a
// few words.
func dashSuffixTitle(t string, e int, m *Map) (titleParse, bool) {
	run := strings.TrimRight(t[:e], " ")
	if e >= len(t) || !(strings.HasSuffix(run, "—") || strings.HasSuffix(run, "–") ||
		strings.HasSuffix(run, "-") || strings.HasSuffix(run, "(")) {
		return titleParse{}, false
	}
	if r, _ := utf8.DecodeRuneInString(t[e:]); !unicode.IsLower(r) {
		return titleParse{}, false
	}
	const maxSuffixWords = 6
	pos := e
	for w := 0; w < maxSuffixWords; w++ {
		// Skip to the next word start.
		next := strings.IndexAny(t[pos:], " \t")
		if next < 0 {
			return titleParse{}, false
		}
		gap := pos + next
		pos = gap
		for pos < len(t) && isSpaceOrTab(t[pos]) {
			pos++
		}
		suffix := t[e:gap]
		if strings.ContainsAny(suffix, ".!?:;") {
			return titleParse{}, false // prose, not a title
		}
		if startsWithCategory(t[pos:], m) {
			// A tab right after the suffix makes the split certain.
			uncertain := !strings.Contains(t[gap:pos], "\t")
			return titleParse{title: t[:gap], fields: t[pos:], uncertain: uncertain}, true
		}
	}
	return titleParse{}, false
}

// looseTitle accepts a caps title followed by a tab, or by a Capitalized
// word at a word boundary. It is used without a category list.
func looseTitle(t string, e int) (titleParse, bool) {
	if r, _ := utf8.DecodeRuneInString(t); !unicode.IsLetter(r) {
		return titleParse{}, false // "1 CUP Bisquick" is an ingredient
	}
	if i := strings.IndexByte(t, '\t'); i >= 0 {
		title := strings.TrimSpace(t[:i])
		fields := strings.TrimSpace(t[i:])
		if upperCount(title) >= 2 && strings.IndexFunc(fields, unicode.IsLetter) >= 0 {
			return titleParse{title: title, fields: fields}, true
		}
		return titleParse{}, false
	}
	for p := e; p > 0; p-- {
		if p >= len(t) || t[p] != ' ' {
			continue
		}
		title := strings.TrimRight(t[:p], " ")
		rest := strings.TrimLeft(t[p:], " ")
		if upperCount(title) >= 2 && startsCapitalizedWord(rest) {
			return titleParse{title: title, fields: rest}, true
		}
	}
	return titleParse{}, false
}

// startsWithCategory reports whether s (after leading blanks) starts with
// a category token followed by a word boundary, not written in all caps.
func startsWithCategory(s string, m *Map) bool {
	s = strings.TrimLeft(s, " \t")
	// Only the first tab field can hold the category.
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	words := strings.Fields(s)
	n, e := m.matchPrefix(words)
	if n == 0 || !e.isCategory() {
		return false
	}
	matched := strings.Join(words[:n], " ")
	if upperCount(matched) >= 2 && !hasLower(matched) && hasLower(e.Token) {
		return false
	}
	return true
}

func startsCapitalizedWord(s string) bool {
	r1, n := utf8.DecodeRuneInString(s)
	r2, _ := utf8.DecodeRuneInString(s[n:])
	return unicode.IsUpper(r1) && unicode.IsLower(r2)
}

func upperCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsUpper(r) {
			n++
		}
	}
	return n
}

func hasLower(s string) bool { return strings.IndexFunc(s, unicode.IsLower) >= 0 }

func isSpaceOrTab(c byte) bool { return c == ' ' || c == '\t' }
