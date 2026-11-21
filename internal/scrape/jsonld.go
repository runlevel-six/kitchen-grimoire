// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"
)

// maxBlock caps one JSON-LD block. Real recipe blocks are a few KiB; a
// bigger one is junk or an attack and is skipped.
const maxBlock = 2 << 20

// maxDepth bounds every recursive walk over decoded JSON. Recipe data is
// shallow; encoding/json allows nesting up to 10000.
const maxDepth = 64

// parseBlock decodes one JSON-LD script body, trying lenient repairs
// (§8.2 step 2) before giving up. A block holding several concatenated
// values decodes to a []any of them.
func parseBlock(s string) (any, bool) {
	if len(s) > maxBlock {
		return nil, false
	}
	s = unwrap(s)
	if s == "" {
		return nil, false
	}
	if v, ok := decode(s); ok {
		return v, true
	}
	repair := func(s string) string { return stripTrailingCommas(escapeControlInStrings(s)) }
	if v, ok := decode(repair(s)); ok {
		return v, true
	}
	// Some sites entity-encode the whole block (&quot;name&quot;: ...).
	if strings.Contains(s, "&quot;") || strings.Contains(s, "&#34;") || strings.Contains(s, "&#x22;") {
		if v, ok := decode(repair(html.UnescapeString(s))); ok {
			return v, true
		}
	}
	return nil, false
}

// unwrap strips whitespace and the HTML comment or CDATA wrappers some
// templates put around script content.
func unwrap(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "\uFEFF")
	for {
		before := s
		s = strings.TrimSpace(s)
		for _, p := range []string{"<!--", "//<![CDATA[", "/*<![CDATA[*/", "<![CDATA["} {
			s = strings.TrimPrefix(s, p)
		}
		for _, p := range []string{"-->", "//]]>", "/*]]>*/", "]]>"} {
			s = strings.TrimSuffix(s, p)
		}
		if s == before {
			return s
		}
	}
}

// decode parses one or more JSON values; trailing semicolons and
// whitespace are tolerated. Numbers stay json.Number so raw_source keeps
// them exactly.
func decode(s string) (any, bool) {
	s = strings.TrimRight(s, "; \t\r\n")
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var vals []any
	for {
		var v any
		err := dec.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		vals = append(vals, v)
	}
	switch len(vals) {
	case 0:
		return nil, false
	case 1:
		return vals[0], true
	}
	return vals, true
}

// escapeControlInStrings escapes raw control characters (newlines, tabs)
// inside JSON string literals, leaving structure untouched. S3 found a
// site that emits raw newlines in instruction text.
func escapeControlInStrings(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr && c < 0x20:
			fmt.Fprintf(&b, `\u%04x`, c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// stripTrailingCommas removes a comma (outside strings) that is followed
// only by whitespace and then } or ].
func stripTrailingCommas(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && c == ',':
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ids indexes the nodes of a document by @id so references like
// {"@id": "#author"} can be followed (§8.2 step 3).
type ids map[string]map[string]any

func (x ids) add(v any, depth int) {
	if depth > maxDepth {
		return
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			x.add(e, depth+1)
		}
	case map[string]any:
		if id, ok := t["@id"].(string); ok && id != "" && !isRef(t) {
			// Keep the fullest description when an @id repeats.
			if old, seen := x[id]; !seen || len(t) > len(old) {
				x[id] = t
			}
		}
		for _, e := range t {
			x.add(e, depth+1)
		}
	}
}

// isRef reports whether m only points at another node: an @id and at most
// an @type.
func isRef(m map[string]any) bool {
	if _, ok := m["@id"].(string); !ok {
		return false
	}
	_, typed := m["@type"]
	return len(m) == 1 || (len(m) == 2 && typed)
}

// deref follows v if it is a reference to a known node; otherwise it
// returns v unchanged.
func (x ids) deref(v any) any {
	m, ok := v.(map[string]any)
	if !ok || !isRef(m) {
		return v
	}
	if n, ok := x[m["@id"].(string)]; ok {
		return n
	}
	return v
}

// findRecipe returns the first node typed Recipe at the top level, in an
// array, or under @graph, mainEntity, or mainEntityOfPage.
func (x ids) findRecipe(v any, depth int) map[string]any {
	if depth > maxDepth {
		return nil
	}
	switch t := x.deref(v).(type) {
	case []any:
		for _, e := range t {
			if r := x.findRecipe(e, depth+1); r != nil {
				return r
			}
		}
	case map[string]any:
		if isType(t["@type"], "Recipe") {
			return t
		}
		for _, k := range []string{"@graph", "mainEntity", "mainEntityOfPage"} {
			if e, ok := t[k]; ok {
				if r := x.findRecipe(e, depth+1); r != nil {
					return r
				}
			}
		}
	}
	return nil
}

// isType reports whether a @type value (string or array) names want, as a
// bare term or a full IRI ("http://schema.org/Recipe", "schema:Recipe").
func isType(v any, want string) bool {
	switch t := v.(type) {
	case string:
		t = strings.TrimSpace(t)
		if i := strings.LastIndexAny(t, "/:#"); i >= 0 {
			t = t[i+1:]
		}
		return strings.EqualFold(t, want)
	case []any:
		for _, e := range t {
			if isType(e, want) {
				return true
			}
		}
	}
	return false
}

// rawSkip lists properties whose references stay unresolved in raw_source:
// they point back at the page and would drag the whole graph in.
var rawSkip = map[string]bool{
	"mainEntityOfPage": true, "isPartOf": true, "mainEntity": true,
	"breadcrumb": true, "potentialAction": true,
}

// inline returns a copy of v with references replaced by the nodes they
// point at, so raw_source is self-contained. A reference already being
// inlined further up (a cycle) is left as is.
func (x ids) inline(v any, stack map[string]bool, depth int) any {
	if depth > maxDepth {
		return v
	}
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = x.inline(e, stack, depth+1)
		}
		return out
	case map[string]any:
		if isRef(t) {
			id := t["@id"].(string)
			n, ok := x[id]
			if !ok || stack[id] {
				return t
			}
			stack[id] = true
			defer delete(stack, id)
			return x.inline(n, stack, depth+1)
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			if rawSkip[k] {
				out[k] = e
				continue
			}
			out[k] = x.inline(e, stack, depth+1)
		}
		return out
	}
	return v
}

// marshalRaw renders v as compact JSON without HTML escaping.
func marshalRaw(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil
	}
	return json.RawMessage(bytes.TrimSpace(b.Bytes()))
}
