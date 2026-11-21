// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// maxItemDepth bounds nested microdata items (Recipe > HowToSection >
// HowToStep is three).
const maxItemDepth = 8

// isRecipeItemtype reports whether an itemtype attribute names
// schema.org/Recipe (http or https, with or without www).
func isRecipeItemtype(v string) bool {
	for _, f := range strings.Fields(v) {
		u, err := url.Parse(f)
		if err != nil {
			continue
		}
		host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		if host == "schema.org" && strings.EqualFold(strings.Trim(u.Path, "/"), "Recipe") {
			return true
		}
	}
	return false
}

// microdataItem converts an itemscope element into the same shape as a
// JSON-LD node: property names map to a string, a nested item (as a map
// with "@type"), or a []any when a property repeats. The field mapping then
// treats both sources alike, and the map doubles as raw_source.
func microdataItem(root *html.Node, depth int) map[string]any {
	props := map[string][]any{}
	walk(root, func(n *html.Node) bool {
		if n == root || n.Type != html.ElementNode {
			return true
		}
		if n.DataAtom == atom.Script || n.DataAtom == atom.Style {
			return false
		}
		names := strings.Fields(attr(n, "itemprop"))
		scoped := hasAttr(n, "itemscope")
		if len(names) > 0 {
			var v any
			switch {
			case scoped && depth < maxItemDepth:
				v = microdataItem(n, depth+1)
			case !scoped:
				v = propValue(n)
			}
			if v != nil {
				for _, name := range names {
					props[name] = append(props[name], v)
				}
			}
		}
		// A nested item's properties belong to that item, not this one.
		return !scoped
	})
	out := make(map[string]any, len(props)+1)
	if t := strings.Fields(attr(root, "itemtype")); len(t) > 0 {
		name := t[0]
		if i := strings.LastIndexAny(name, "/#"); i >= 0 {
			name = name[i+1:]
		}
		out["@type"] = name
	}
	for k, vs := range props {
		if len(vs) == 1 {
			out[k] = vs[0]
		} else {
			out[k] = vs
		}
	}
	// A step item often marks no text property: its text is the element's.
	if t, _ := out["@type"].(string); strings.HasPrefix(t, "HowTo") && t != "HowToSection" {
		if _, ok := out["text"]; !ok {
			out["text"] = tidyBlock(nodeText(root))
		}
	}
	return out
}

// propValue is a microdata property's value: an explicit content attribute
// wins (common for times: <span itemprop="prepTime" content="PT10M">),
// then the element-specific attribute, then the text content.
func propValue(n *html.Node) any {
	if hasAttr(n, "content") {
		return attr(n, "content")
	}
	switch n.DataAtom {
	case atom.Img, atom.Audio, atom.Video, atom.Source, atom.Embed, atom.Iframe, atom.Track:
		return attr(n, "src")
	case atom.A, atom.Area, atom.Link:
		return attr(n, "href")
	case atom.Object:
		return attr(n, "data")
	case atom.Data, atom.Meter:
		return attr(n, "value")
	case atom.Time:
		if hasAttr(n, "datetime") {
			return attr(n, "datetime")
		}
	}
	return tidyBlock(nodeText(n))
}
