// SPDX-License-Identifier: Apache-2.0

package recipe

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/runlevel-six/kitchen-grimoire/internal/ingredient"
)

// Limits keep a single recipe to a sane size. They are generous: the
// largest legacy recipes are far below them.
const (
	MaxTitle       = 200
	MaxShortText   = 300 // yield, author, contributor, collection, group names
	MaxDescription = 5000
	MaxNotes       = 50000
	MaxLine        = 1000 // one ingredient line
	MaxStep        = 10000
	MaxLines       = 500 // ingredient lines or steps
	MaxGroups      = 50
	MaxTags        = 50
	MaxTag         = 50
	MaxCategories  = 20
	MaxCategoryLen = 100
	MaxCatDepth    = 3
	MaxMinutes     = 60 * 24 * 14 // two weeks covers curing and fermenting
	MaxServings    = 10000
	MaxURL         = 2000
)

// ValidationError lists problems by field, for showing next to form inputs.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e.Fields[k]
	}
	return "invalid recipe: " + strings.Join(parts, "; ")
}

// Normalize tidies a recipe in place before validation: trims text, drops
// blank ingredient lines, steps, and empty groups, (re)parses ingredient
// lines from their raw text, and de-duplicates tags and categories. It never
// changes wording.
func Normalize(r *Recipe) {
	r.Title = cleanLine(r.Title)
	r.Description = cleanBlock(r.Description)
	r.YieldText = cleanLine(r.YieldText)
	r.Notes = cleanBlock(r.Notes)
	r.SourceURL = strings.TrimSpace(r.SourceURL)
	r.CanonicalURL = strings.TrimSpace(r.CanonicalURL)
	r.SourceSite = cleanLine(r.SourceSite)
	r.Author = cleanLine(r.Author)
	r.ContributedBy = cleanLine(r.ContributedBy)
	r.Collection = cleanLine(r.Collection)
	r.ImportKey = strings.TrimSpace(r.ImportKey)
	if r.SourceKind == "" {
		r.SourceKind = SourceManual
	}

	var igs []IngredientGroup
	for _, g := range r.IngredientGroups {
		g.Name = cleanLine(g.Name)
		var lines []Ingredient
		for _, in := range g.Ingredients {
			raw := cleanLine(in.Raw)
			if raw == "" {
				continue
			}
			lines = append(lines, ingredient.Parse(raw))
		}
		if len(lines) == 0 {
			continue // a named group with no lines carries nothing
		}
		g.Ingredients = lines
		igs = append(igs, g)
	}
	r.IngredientGroups = igs

	var sgs []StepGroup
	for _, g := range r.StepGroups {
		g.Name = cleanLine(g.Name)
		var steps []string
		for _, s := range g.Steps {
			if s = cleanBlock(s); s != "" {
				steps = append(steps, s)
			}
		}
		if len(steps) == 0 {
			continue
		}
		g.Steps = steps
		sgs = append(sgs, g)
	}
	r.StepGroups = sgs

	seenTag := map[string]bool{}
	var tags []string
	for _, t := range r.Tags {
		t = cleanLine(t)
		key := Slugify(t)
		if t == "" || key == "" || seenTag[key] {
			continue
		}
		seenTag[key] = true
		tags = append(tags, t)
	}
	r.Tags = tags

	seenCat := map[string]bool{}
	var cats [][]string
	for _, path := range r.Categories {
		var clean []string
		for _, name := range path {
			if name = cleanLine(name); name != "" {
				clean = append(clean, name)
			}
		}
		key := CategoryPathSlug(clean)
		if len(clean) == 0 || seenCat[key] {
			continue
		}
		seenCat[key] = true
		cats = append(cats, clean)
	}
	r.Categories = cats

	var reasons []string
	for _, rr := range r.ReviewReasons {
		if rr = strings.TrimSpace(rr); rr != "" && !slices.Contains(reasons, rr) {
			reasons = append(reasons, rr)
		}
	}
	r.ReviewReasons = reasons
	if len(reasons) > 0 {
		r.NeedsReview = true
	}
}

// Validate checks a normalized recipe. It returns a *ValidationError or nil.
func Validate(r *Recipe) error {
	f := map[string]string{}
	add := func(field, msg string) {
		if _, ok := f[field]; !ok {
			f[field] = msg
		}
	}
	tooLong := func(field, s string, max int) {
		if utf8.RuneCountInString(s) > max {
			add(field, fmt.Sprintf("is too long (max %d characters)", max))
		}
	}

	if r.Title == "" {
		add("title", "is required")
	}
	tooLong("title", r.Title, MaxTitle)
	tooLong("description", r.Description, MaxDescription)
	tooLong("yieldText", r.YieldText, MaxShortText)
	tooLong("notes", r.Notes, MaxNotes)
	tooLong("author", r.Author, MaxShortText)
	tooLong("contributedBy", r.ContributedBy, MaxShortText)
	tooLong("collection", r.Collection, MaxShortText)
	tooLong("sourceSite", r.SourceSite, MaxShortText)
	tooLong("importKey", r.ImportKey, MaxShortText)

	if !r.SourceKind.Valid() {
		add("sourceKind", "is not a known source")
	}
	if r.Servings < 0 || r.Servings > MaxServings {
		add("servings", fmt.Sprintf("must be between 0 and %d", MaxServings))
	}
	for field, m := range map[string]int{"prepMin": r.PrepMin, "cookMin": r.CookMin, "totalMin": r.TotalMin} {
		if m < 0 || m > MaxMinutes {
			add(field, "is out of range")
		}
	}
	for field, u := range map[string]string{"sourceUrl": r.SourceURL, "canonicalUrl": r.CanonicalURL} {
		if u == "" {
			continue
		}
		if len(u) > MaxURL {
			add(field, "is too long")
			continue
		}
		pu, err := url.Parse(u)
		if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			add(field, "must be a full http(s) address")
		}
	}

	if len(r.IngredientGroups) > MaxGroups {
		add("ingredients", fmt.Sprintf("has too many groups (max %d)", MaxGroups))
	}
	if r.IngredientCount() > MaxLines {
		add("ingredients", fmt.Sprintf("has too many lines (max %d)", MaxLines))
	}
	for _, g := range r.IngredientGroups {
		tooLong("ingredients", g.Name, MaxShortText)
		for _, in := range g.Ingredients {
			tooLong("ingredients", in.Raw, MaxLine)
		}
	}
	if len(r.StepGroups) > MaxGroups {
		add("steps", fmt.Sprintf("has too many groups (max %d)", MaxGroups))
	}
	if r.StepCount() > MaxLines {
		add("steps", fmt.Sprintf("has too many steps (max %d)", MaxLines))
	}
	for _, g := range r.StepGroups {
		tooLong("steps", g.Name, MaxShortText)
		for _, s := range g.Steps {
			tooLong("steps", s, MaxStep)
		}
	}

	if len(r.Tags) > MaxTags {
		add("tags", fmt.Sprintf("has too many tags (max %d)", MaxTags))
	}
	for _, t := range r.Tags {
		tooLong("tags", t, MaxTag)
	}
	if len(r.Categories) > MaxCategories {
		add("categories", fmt.Sprintf("has too many categories (max %d)", MaxCategories))
	}
	for _, p := range r.Categories {
		if len(p) > MaxCatDepth {
			add("categories", fmt.Sprintf("can be at most %d levels deep", MaxCatDepth))
		}
		for _, name := range p {
			tooLong("categories", name, MaxCategoryLen)
		}
	}

	if len(f) > 0 {
		return &ValidationError{Fields: f}
	}
	return nil
}

// cleanLine trims and collapses internal whitespace runs (including tabs
// and newlines) to single spaces, and drops control characters.
func cleanLine(s string) string {
	return strings.Join(strings.FieldsFunc(stripControl(s), unicode.IsSpace), " ")
}

// cleanBlock trims each line's trailing space, normalizes line endings,
// drops control characters other than newlines, and trims blank lines at
// both ends. Inner blank lines (paragraphs) are kept.
func cleanBlock(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRightFunc(stripControl(strings.ReplaceAll(l, "\t", " ")), unicode.IsSpace)
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n ")
}

func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
