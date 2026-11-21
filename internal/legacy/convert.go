// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"fmt"
	"strings"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// Options configure a conversion.
type Options struct {
	Format     Format
	Map        *Map
	Normalizer *Normalizer // nil: built-in rules only
	Collection string      // e.g. "Example Family Cookbook"
	// KeySuffix is appended to the collection slug in import keys. The
	// older edition uses "older", so its keys never collide with the
	// primary edition's ordinals.
	KeySuffix string
}

// Draft is one converted recipe, ready for the bundle writer.
type Draft struct {
	Recipe  recipe.Recipe
	Ordinal int // 1-based position in its source
	// Older is the same recipe in the older edition (D-13), to be stored
	// as an earlier revision. Set by Merge.
	Older *Draft
	// OlderOnly marks a recipe found only in the older edition.
	OlderOnly bool
	// MatchScore is the title similarity of a fuzzy edition match (1 for
	// an exact match, 0 when unmatched).
	MatchScore float64
}

// Result is a converted document.
type Result struct {
	Format      Format
	PrefixLines int // lines before the first title, ignored
	Drafts      []Draft
}

// Convert splits text and turns every block into a draft.
func Convert(text string, opt Options) *Result {
	doc := Split(text, opt.Format, opt.Map)
	res := &Result{Format: doc.Format, PrefixLines: doc.PrefixLines}
	slug := recipe.Slugify(opt.Collection)
	if opt.KeySuffix != "" {
		slug += "-" + opt.KeySuffix
	}
	for _, b := range doc.Blocks {
		res.Drafts = append(res.Drafts, convertBlock(b, doc.Format, slug, opt))
	}
	return res
}

// ImportKey is the stable idempotency key of a legacy recipe (§8.4).
func ImportKey(collectionSlug string, ordinal int, title string) string {
	return fmt.Sprintf("legacy:%s:%04d:%s", collectionSlug, ordinal, recipe.Slugify(title))
}

func convertBlock(b Block, format Format, slug string, opt Options) Draft {
	r := recipe.Recipe{
		Title:        b.Title,
		SourceKind:   recipe.SourceLegacy,
		Collection:   opt.Collection,
		ImportKey:    ImportKey(slug, b.Ordinal, b.Title),
		OriginalText: visibleText(append([]srcLine{b.head}, b.lines...)),
	}
	var reasons []string
	if b.TitleUncertain {
		reasons = append(reasons, ReasonTitleUncertain)
	}

	lines := b.lines
	fields := b.Fields
	// PDF text can wrap a long title line: a first body line made only of
	// known tokens continues the fields.
	if format == FormatPDFText {
		for len(lines) > 0 && (lines[0].marker || allTokens(lines[0].text, opt.Map)) {
			if !lines[0].marker {
				fields += " " + strings.TrimSpace(lines[0].text)
			}
			lines = lines[1:]
		}
	}

	toks, tabbed := fieldTokens(fields, opt.Map)
	a := classify(toks, tabbed)
	r.Categories = a.categories
	r.Tags = a.tags
	r.ContributedBy = strings.Join(a.contributors, ", ")
	r.Author = strings.Join(a.authors, ", ")
	r.Notes = strings.Join(a.notes, "\n")
	reasons = append(reasons, a.reasons...)

	bd := parseBody(lines, format, opt.Map, opt.Normalizer)
	r.IngredientGroups = bd.groups
	if len(bd.steps) > 0 {
		r.StepGroups = []recipe.StepGroup{{Steps: bd.steps}}
	}
	reasons = append(reasons, bd.reasons...)
	if r.IngredientCount() == 0 {
		reasons = append(reasons, ReasonNoIngredients)
	}
	if r.StepCount() == 0 {
		reasons = append(reasons, ReasonNoSteps)
	}

	// A line inside the block that would be a title under the loose rules
	// probably starts a recipe whose category the map doesn't know yet.
	for _, l := range lines {
		if l.marker {
			continue
		}
		if _, ok := parseTitle(l.text, opt.Map, true); ok {
			reasons = append(reasons, ReasonPossibleTitle)
			break
		}
	}

	// Validate a normalized copy, so a draft the importer would reject is
	// flagged here rather than failing the whole bundle later.
	check := r
	recipe.Normalize(&check)
	if err := recipe.Validate(&check); err != nil {
		reasons = append(reasons, "invalid:"+err.Error())
	}

	d := Draft{Recipe: r, Ordinal: b.Ordinal}
	for _, rs := range reasons {
		d.flag(rs)
	}
	return d
}

// flag adds a review reason once and marks the recipe for review.
func (d *Draft) flag(reason string) {
	for _, r := range d.Recipe.ReviewReasons {
		if r == reason {
			return
		}
	}
	d.Recipe.ReviewReasons = append(d.Recipe.ReviewReasons, reason)
	d.Recipe.NeedsReview = true
}

// allTokens reports whether a line is made entirely of known map tokens
// (and has no digits, so an ingredient line never qualifies).
func allTokens(line string, m *Map) bool {
	if strings.TrimSpace(line) == "" || strings.ContainsAny(line, "0123456789") {
		return false
	}
	for _, t := range m.splitWords(line) {
		if t.Entry == nil {
			return false
		}
	}
	return true
}
