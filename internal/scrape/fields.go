// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// mapper turns a schema.org Recipe node (decoded JSON-LD, or microdata in
// the same shape) into a draft recipe (§8.2 step 4).
type mapper struct {
	ids  ids      // @id index for following references; nil for microdata
	base *url.URL // page URL, for resolving image addresses
	// plain is set for microdata, whose values are already text taken from
	// the DOM and must not be parsed as HTML a second time.
	plain bool
}

func (m *mapper) get(n map[string]any, key string) any { return m.ids.deref(n[key]) }

// block converts a string value to plain text paragraphs.
func (m *mapper) block(s string) string {
	if m.plain {
		return tidyBlock(s)
	}
	return plainBlock(s)
}

func (m *mapper) line(s string) string { return collapse(m.block(s)) }

// str extracts a string from a scalar, the name/text/@value of an object,
// or the first non-empty element of an array.
func (m *mapper) str(v any, depth int) string {
	if depth > maxDepth {
		return ""
	}
	switch t := m.ids.deref(v).(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case map[string]any:
		for _, k := range []string{"name", "text", "@value", "value"} {
			if s := m.str(t[k], depth+1); strings.TrimSpace(s) != "" {
				return s
			}
		}
	case []any:
		for _, e := range t {
			if s := m.str(e, depth+1); strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// strs flattens a string or array into its string values.
func (m *mapper) strs(v any) []string {
	switch t := m.ids.deref(v).(type) {
	case nil:
		return nil
	case []any:
		var out []string
		for _, e := range t {
			if s := m.str(e, 0); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		if s := m.str(t, 0); s != "" {
			return []string{s}
		}
	}
	return nil
}

func (m *mapper) recipe(n map[string]any) (*recipe.Recipe, []string) {
	r := &recipe.Recipe{}
	r.Title = m.line(m.str(m.get(n, "name"), 0))
	if r.Title == "" {
		r.Title = m.line(m.str(m.get(n, "headline"), 0))
	}
	r.Description = m.block(m.str(m.get(n, "description"), 0))
	r.YieldText, r.Servings = m.yield(m.get(n, "recipeYield"))
	r.PrepMin = m.minutes(m.get(n, "prepTime"))
	r.CookMin = m.minutes(m.get(n, "cookTime"))
	r.TotalMin = m.minutes(m.get(n, "totalTime"))

	ing := m.get(n, "recipeIngredient")
	if ing == nil {
		ing = m.get(n, "ingredients") // the older schema.org name
	}
	var lines []recipe.Ingredient
	for _, s := range m.strs(ing) {
		// One value may hold several lines (<li>s or newlines).
		for _, l := range strings.Split(m.block(s), "\n") {
			if l = collapse(l); l != "" {
				lines = append(lines, recipe.Ingredient{Raw: l})
			}
		}
	}
	if len(lines) > 0 {
		r.IngredientGroups = []recipe.IngredientGroup{{Ingredients: lines}}
	}

	r.StepGroups = m.instructions(m.get(n, "recipeInstructions"))

	for _, c := range m.list(m.get(n, "recipeCategory")) {
		r.Categories = append(r.Categories, []string{c})
	}
	r.Tags = m.list(m.get(n, "keywords"))
	r.Author = m.author(m.get(n, "author"))
	return r, m.images(m.get(n, "image"))
}

// minutes reads a duration property; unreadable or absurd values are 0.
func (m *mapper) minutes(v any) int {
	d, ok := ParseISODuration(m.line(m.str(v, 0)))
	if !ok {
		return 0
	}
	if n := minutes(d); n <= recipe.MaxMinutes {
		return n
	}
	return 0
}

var numberRE = regexp.MustCompile(`\d+(?:\.\d+)?`)

// yield picks the most descriptive string (the longest one with a letter
// in it, else the first) for display, and the first number for scaling.
func (m *mapper) yield(v any) (string, float64) {
	var vals []any
	switch t := v.(type) {
	case []any:
		vals = t
	case nil:
	default:
		vals = []any{t}
	}
	var text, first string
	var servings float64
	for _, e := range vals {
		e = m.ids.deref(e)
		if num, ok := e.(json.Number); ok {
			if f, err := num.Float64(); err == nil && servings == 0 && f > 0 && f <= recipe.MaxServings {
				servings = f
			}
			if first == "" {
				first = num.String()
			}
			continue
		}
		s := m.line(m.str(e, 0))
		if s == "" {
			continue
		}
		if first == "" {
			first = s
		}
		if strings.IndexFunc(s, unicode.IsLetter) >= 0 && len(s) > len(text) {
			text = s
		}
		if servings == 0 {
			if f, err := strconv.ParseFloat(numberRE.FindString(s), 64); err == nil && f > 0 && f <= recipe.MaxServings {
				servings = f
			}
		}
	}
	if text == "" {
		text = first
	}
	return text, servings
}

// list reads recipeCategory or keywords: a comma-separated string or an
// array of them, trimmed and de-duplicated ignoring case.
func (m *mapper) list(v any) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range m.strs(v) {
		for _, part := range strings.Split(m.line(s), ",") {
			part = collapse(part)
			key := strings.ToLower(part)
			if part == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, part)
		}
	}
	return out
}

// author joins the names of one or more authors given as strings, Person
// or Organization objects, or an array of either.
func (m *mapper) author(v any) string {
	var names []string
	add := func(e any) {
		e = m.ids.deref(e)
		var s string
		if o, ok := e.(map[string]any); ok {
			s = m.str(m.ids.deref(o["name"]), 0)
		} else {
			s = m.str(e, 0)
		}
		if s = m.line(s); s != "" && !containsFold(names, s) {
			names = append(names, s)
		}
	}
	if a, ok := v.([]any); ok {
		for _, e := range a {
			add(e)
		}
	} else if v != nil {
		add(v)
	}
	return strings.Join(names, ", ")
}

func containsFold(list []string, s string) bool {
	for _, e := range list {
		if strings.EqualFold(e, s) {
			return true
		}
	}
	return false
}

// images lists image addresses, absolute, best first: those with a
// declared size by area (largest first), then the rest in document order.
func (m *mapper) images(v any) []string {
	type cand struct {
		url  string
		area int
		pos  int
	}
	var cands []cand
	var add func(e any, depth int)
	add = func(e any, depth int) {
		if depth > 4 {
			return
		}
		switch t := m.ids.deref(e).(type) {
		case string:
			cands = append(cands, cand{url: t, pos: len(cands)})
		case []any:
			for _, x := range t {
				add(x, depth+1)
			}
		case map[string]any:
			u := m.str(t["url"], 0)
			if u == "" {
				u = m.str(t["contentUrl"], 0)
			}
			if u == "" {
				return
			}
			w, h := dimension(m.str(t["width"], 0)), dimension(m.str(t["height"], 0))
			cands = append(cands, cand{url: u, area: w * h, pos: len(cands)})
		}
	}
	add(v, 0)
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].area > cands[j].area })
	var out []string
	for _, c := range cands {
		u := resolve(m.base, html2url(c.url))
		if u == nil {
			continue
		}
		s := u.String()
		if len(s) <= recipe.MaxURL && !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// html2url decodes entities in an address taken from JSON ("&amp;" in a
// query string is common).
func html2url(s string) string {
	if strings.Contains(s, "&") {
		s = entityRE.ReplaceAllStringFunc(s, func(e string) string { return plainLine(e) })
	}
	return strings.TrimSpace(s)
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// dimension reads a width or height such as 1200, "1200", or "1200px".
func dimension(s string) int {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && i < 6 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}
