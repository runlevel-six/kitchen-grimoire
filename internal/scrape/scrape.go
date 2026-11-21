// SPDX-License-Identifier: Apache-2.0

// Package scrape extracts a draft recipe from a fetched HTML page
// (docs/DESIGN.md §8.2 steps 2–7 and the canonical URL of step 10). It
// reads schema.org Recipe data from JSON-LD, falls back to Microdata, and
// finally to an OpenGraph stub flagged for review. It is pure parsing and
// never touches the network: the caller fetches the page (and images) with
// safefetch.
package scrape

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// Method says which kind of page data a recipe came from.
type Method string

const (
	MethodJSONLD    Method = "jsonld"
	MethodMicrodata Method = "microdata"
	MethodOpenGraph Method = "opengraph_stub"
)

// Review reasons set on drafts that need a human look.
const (
	ReasonNoStructuredData = "no_structured_data"
	ReasonNoIngredients    = "no_ingredients"
	ReasonNoSteps          = "no_steps"
	ReasonNoTitle          = "no_title"
	ReasonTruncated        = "truncated"
)

// ErrNoRecipe means the page has no recipe data and not even a title to
// build a stub from.
var ErrNoRecipe = errors.New("scrape: no recipe found on page")

// Result is one page's extraction.
type Result struct {
	// Recipe is the draft: provenance, text fields, one unnamed ingredient
	// group of raw lines, and step groups. It is not normalized; the
	// ingest pipeline does that.
	Recipe *recipe.Recipe
	// RawSource is the extracted Recipe object with @id references
	// inlined, or the Microdata properties rendered as JSON; nil for an
	// OpenGraph stub. Also set as Recipe.RawSource (§7.1).
	RawSource json.RawMessage
	Method    Method
	// ImageURLs are absolute http(s) addresses, best (largest) first.
	ImageURLs []string
	// CanonicalURL is the dedupe key (see CanonicalURL); also set on Recipe.
	CanonicalURL string
}

// Extract parses page, fetched from pageURL (the final URL after
// redirects; it may be nil), and returns the best recipe it can find. It
// returns ErrNoRecipe only when the page has no structured recipe data and
// no title at all; that is the only error it returns.
func Extract(page []byte, pageURL *url.URL) (*Result, error) {
	var p *pageInfo
	if doc, err := html.Parse(bytes.NewReader(page)); err == nil {
		p = scanPage(doc)
	} else {
		p = scanTokens(page)
	}
	res := &Result{}

	if node, x := findJSONLD(p.ldBlocks); node != nil {
		m := &mapper{ids: x, base: pageURL}
		res.Recipe, res.ImageURLs = m.recipe(node)
		stack := map[string]bool{}
		if id, ok := node["@id"].(string); ok {
			stack[id] = true // a reference back to the recipe stays a reference
		}
		res.RawSource = marshalRaw(x.inline(node, stack, 0))
		res.Method = MethodJSONLD
	} else if p.microdata != nil {
		node := microdataItem(p.microdata, 0)
		node["@type"] = "Recipe"
		m := &mapper{base: pageURL, plain: true}
		res.Recipe, res.ImageURLs = m.recipe(node)
		res.RawSource = marshalRaw(node)
		res.Method = MethodMicrodata
	} else {
		title := plainLine(p.meta["og:title"])
		if title == "" {
			title = p.title
		}
		if title == "" {
			return nil, ErrNoRecipe
		}
		desc := p.meta["og:description"]
		if desc == "" {
			desc = p.meta["description"]
		}
		res.Recipe = &recipe.Recipe{
			Title:         title,
			Description:   plainBlock(desc),
			NeedsReview:   true,
			ReviewReasons: []string{ReasonNoStructuredData},
		}
		res.Method = MethodOpenGraph
	}

	finish(res, p, pageURL)
	return res, nil
}

// findJSONLD decodes every block and returns the first Recipe node with
// the @id index built over all blocks (references may cross blocks).
func findJSONLD(blocks []string) (map[string]any, ids) {
	var vals []any
	x := ids{}
	for _, b := range blocks {
		if v, ok := parseBlock(b); ok {
			vals = append(vals, v)
			x.add(v, 0)
		}
	}
	for _, v := range vals {
		if n := x.findRecipe(v, 0); n != nil {
			return n, x
		}
	}
	return nil, x
}

// finish fills provenance and fallbacks shared by every method, flags
// what needs review, and trims the draft to the recipe limits so that it
// passes recipe.Validate.
func finish(res *Result, p *pageInfo, pageURL *url.URL) {
	r := res.Recipe
	r.SourceKind = recipe.SourceURL
	if httpURL(pageURL) {
		if s := pageURL.String(); len(s) <= recipe.MaxURL {
			r.SourceURL = s
		}
		r.SourceSite = strings.TrimPrefix(strings.ToLower(pageURL.Hostname()), "www.")
	}
	res.CanonicalURL = canonicalURL(p.canonical, pageURL)
	r.CanonicalURL = res.CanonicalURL
	r.RawSource = res.RawSource

	if r.Title == "" {
		r.Title = plainLine(p.meta["og:title"])
	}
	if r.Title == "" {
		r.Title = p.title
	}
	if r.Title == "" {
		r.Title = "Untitled recipe"
		r.ReviewReasons = append(r.ReviewReasons, ReasonNoTitle)
	}
	if r.Description == "" && res.Method != MethodOpenGraph {
		r.Description = plainBlock(p.meta["og:description"])
	}
	if len(res.ImageURLs) == 0 {
		if u := resolve(pageURL, html2url(p.meta["og:image"])); u != nil && len(u.String()) <= recipe.MaxURL {
			res.ImageURLs = []string{u.String()}
		}
	}
	if res.Method != MethodOpenGraph {
		if r.IngredientCount() == 0 {
			r.ReviewReasons = append(r.ReviewReasons, ReasonNoIngredients)
		}
		if r.StepCount() == 0 {
			r.ReviewReasons = append(r.ReviewReasons, ReasonNoSteps)
		}
	}
	if clamp(r) {
		r.ReviewReasons = append(r.ReviewReasons, ReasonTruncated)
	}
	r.NeedsReview = len(r.ReviewReasons) > 0
}

// clamp cuts the draft to the limits recipe.Validate enforces and reports
// whether anything was lost.
func clamp(r *recipe.Recipe) bool {
	cut := false
	trunc := func(s *string, n int) {
		var c bool
		if *s, c = truncate(*s, n); c {
			cut = true
		}
	}
	trunc(&r.Title, recipe.MaxTitle)
	trunc(&r.Description, recipe.MaxDescription)
	trunc(&r.YieldText, recipe.MaxShortText)
	trunc(&r.Author, recipe.MaxShortText)
	trunc(&r.SourceSite, recipe.MaxShortText)

	lines := 0
	for gi := range r.IngredientGroups {
		g := &r.IngredientGroups[gi]
		if lines+len(g.Ingredients) > recipe.MaxLines {
			g.Ingredients = g.Ingredients[:recipe.MaxLines-lines]
			cut = true
		}
		lines += len(g.Ingredients)
		for i := range g.Ingredients {
			trunc(&g.Ingredients[i].Raw, recipe.MaxLine)
		}
	}

	if len(r.StepGroups) > recipe.MaxGroups {
		r.StepGroups = r.StepGroups[:recipe.MaxGroups]
		cut = true
	}
	steps := 0
	var groups []recipe.StepGroup
	for _, g := range r.StepGroups {
		if steps+len(g.Steps) > recipe.MaxLines {
			g.Steps = g.Steps[:recipe.MaxLines-steps]
			cut = true
		}
		if len(g.Steps) == 0 {
			continue
		}
		steps += len(g.Steps)
		trunc(&g.Name, recipe.MaxShortText)
		for i := range g.Steps {
			trunc(&g.Steps[i], recipe.MaxStep)
		}
		groups = append(groups, g)
	}
	r.StepGroups = groups

	// Over-long tags and category names are junk rather than worth
	// truncating; extra ones are dropped.
	var tags []string
	for _, t := range r.Tags {
		if len([]rune(t)) <= recipe.MaxTag && len(tags) < recipe.MaxTags {
			tags = append(tags, t)
		} else {
			cut = true
		}
	}
	r.Tags = tags
	var cats [][]string
	for _, c := range r.Categories {
		if len(c) == 1 && len([]rune(c[0])) <= recipe.MaxCategoryLen && len(cats) < recipe.MaxCategories {
			cats = append(cats, c)
		} else {
			cut = true
		}
	}
	r.Categories = cats
	return cut
}
