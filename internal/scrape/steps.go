// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// instructions maps recipeInstructions to step groups. It accepts a string
// (split into paragraphs), an array of strings, HowToStep objects, and
// HowToSection objects whose itemListElement holds steps or further
// sections, in any mix. Sections become named groups; nested sections are
// flattened, each named by its own name (or its parent's when it has
// none). Loose steps go to unnamed groups, keeping document order.
func (m *mapper) instructions(v any) []recipe.StepGroup {
	c := &stepCollector{m: m}
	if s, ok := v.(string); ok {
		for _, p := range paragraphs(m.block(s)) {
			c.add("", p)
		}
		return c.groups
	}
	c.walk(v, "", 0)
	return c.groups
}

type stepCollector struct {
	m      *mapper
	groups []recipe.StepGroup
}

// add appends a step to the last group if it has the same name, else
// starts a new group.
func (c *stepCollector) add(group, text string) {
	if text == "" || text == group {
		return // empty, or a section heading repeated as a step
	}
	if n := len(c.groups); n > 0 && c.groups[n-1].Name == group {
		c.groups[n-1].Steps = append(c.groups[n-1].Steps, text)
		return
	}
	c.groups = append(c.groups, recipe.StepGroup{Name: group, Steps: []string{text}})
}

func (c *stepCollector) walk(v any, section string, depth int) {
	if depth > maxDepth {
		return
	}
	m := c.m
	switch t := m.ids.deref(v).(type) {
	case []any:
		for _, e := range t {
			c.walk(e, section, depth+1)
		}
	case string:
		c.add(section, m.line(t))
	case map[string]any:
		items, hasItems := t["itemListElement"]
		text := m.line(m.str(t["text"], 0))
		switch {
		case isType(t["@type"], "HowToSection") || (hasItems && text == ""):
			// A section, or an ItemList/HowToStep that only wraps others.
			name := section
			if isType(t["@type"], "HowToSection") || isType(t["@type"], "ItemList") {
				if n := m.line(m.str(t["name"], 0)); n != "" {
					name = n
				}
			}
			c.walk(items, name, depth+1)
		default:
			// A HowToStep (or untyped step). Prefer text; name is often
			// just a truncated copy of it and is used only when text is
			// missing.
			if text == "" {
				text = m.line(m.str(t["name"], 0))
			}
			c.add(section, text)
		}
	}
}
