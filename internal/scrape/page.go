// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// pageInfo is what one pass over the parsed document collects.
type pageInfo struct {
	ldBlocks  []string          // raw text of each JSON-LD script, in document order
	microdata *html.Node        // first itemscope element typed schema.org/Recipe
	meta      map[string]string // first og:* value per property, plus "description"
	title     string            // first <title>
	canonical string            // first <link rel=canonical> href, unresolved
}

func scanPage(doc *html.Node) *pageInfo {
	p := &pageInfo{meta: map[string]string{}}
	walk(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		// Foreign content (<svg><title>) is not the page's metadata.
		if n.Namespace != "" {
			return false
		}
		switch n.DataAtom {
		case atom.Script:
			if strings.EqualFold(strings.TrimSpace(attr(n, "type")), "application/ld+json") {
				var b strings.Builder
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.TextNode {
						b.WriteString(c.Data)
					}
				}
				p.ldBlocks = append(p.ldBlocks, b.String())
			}
			return false
		case atom.Meta:
			key := strings.ToLower(strings.TrimSpace(attr(n, "property")))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(attr(n, "name")))
			}
			if strings.HasPrefix(key, "og:") || key == "description" {
				if _, seen := p.meta[key]; !seen {
					if v := strings.TrimSpace(attr(n, "content")); v != "" {
						p.meta[key] = v
					}
				}
			}
		case atom.Title:
			if p.title == "" {
				p.title = collapse(nodeText(n))
			}
		case atom.Link:
			if p.canonical == "" && hasToken(attr(n, "rel"), "canonical") {
				p.canonical = strings.TrimSpace(attr(n, "href"))
			}
		}
		if p.microdata == nil && hasAttr(n, "itemscope") && isRecipeItemtype(attr(n, "itemtype")) {
			p.microdata = n
		}
		return true
	})
	return p
}

// scanTokens is the fallback when html.Parse refuses a page (it gives up
// on elements nested more than 512 deep). It streams tokens and collects
// everything scanPage does except Microdata, which needs the tree.
func scanTokens(b []byte) *pageInfo {
	p := &pageInfo{meta: map[string]string{}}
	z := html.NewTokenizer(bytes.NewReader(b))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return p
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		attrs := map[string]string{}
		for hasAttr {
			var k, v []byte
			k, v, hasAttr = z.TagAttr()
			if _, seen := attrs[string(k)]; !seen {
				attrs[string(k)] = string(v)
			}
		}
		switch string(name) {
		case "script":
			ld := strings.EqualFold(strings.TrimSpace(attrs["type"]), "application/ld+json")
			if z.Next() == html.TextToken && ld && tt == html.StartTagToken {
				p.ldBlocks = append(p.ldBlocks, string(z.Text()))
			}
		case "meta":
			key := strings.ToLower(strings.TrimSpace(attrs["property"]))
			if key == "" {
				key = strings.ToLower(strings.TrimSpace(attrs["name"]))
			}
			if v := strings.TrimSpace(attrs["content"]); v != "" && (strings.HasPrefix(key, "og:") || key == "description") {
				if _, seen := p.meta[key]; !seen {
					p.meta[key] = v
				}
			}
		case "title":
			if p.title == "" && z.Next() == html.TextToken {
				p.title = collapse(string(z.Text()))
			}
		case "link":
			if p.canonical == "" && hasToken(attrs["rel"], "canonical") {
				p.canonical = strings.TrimSpace(attrs["href"])
			}
		}
	}
}

// walk visits root and its descendants in document order. visit returns
// false to skip a node's children. It is iterative because a hostile page
// can nest elements arbitrarily deep.
func walk(root *html.Node, visit func(*html.Node) bool) {
	n := root
	for n != nil {
		if visit(n) && n.FirstChild != nil {
			n = n.FirstChild
			continue
		}
		for n != root && n.NextSibling == nil {
			n = n.Parent
		}
		if n == root {
			return
		}
		n = n.NextSibling
	}
}

// nodeText returns the text content of n with block elements separated by
// blank lines and <br> as a line break, like htmlToText does for strings.
func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) bool {
		switch c.Type {
		case html.TextNode:
			b.WriteString(c.Data)
		case html.ElementNode:
			switch {
			case c.DataAtom == atom.Script || c.DataAtom == atom.Style:
				return false
			case c.DataAtom == atom.Br:
				b.WriteByte('\n')
			case blockTags[c.Data]:
				b.WriteString("\n\n")
			}
		}
		return true
	})
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

// hasToken reports whether the space-separated list s contains tok,
// ignoring case (rel="Canonical", itemprop="name description").
func hasToken(s, tok string) bool {
	for _, f := range strings.Fields(s) {
		if strings.EqualFold(f, tok) {
			return true
		}
	}
	return false
}
