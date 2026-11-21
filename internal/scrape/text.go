// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// Scraped text arrives as HTML fragments more often than not: JSON-LD
// fields with <p> and &amp; inside, sometimes entity-encoded twice. These
// helpers reduce any of that to plain text (§8.2 step 5).

// blockTags end a paragraph-like block; text on either side becomes
// separate paragraphs ("\n\n").
var blockTags = map[string]bool{
	"p": true, "div": true, "li": true, "ul": true, "ol": true, "section": true,
	"article": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "blockquote": true, "tr": true, "table": true, "dd": true, "dt": true,
	"header": true, "footer": true, "figure": true, "figcaption": true, "pre": true,
}

// entityRE matches one well-formed character reference, used to undo a
// second layer of encoding ("&amp;#8217;" decodes once to "&#8217;").
var entityRE = regexp.MustCompile(`&(?:#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[a-zA-Z][a-zA-Z0-9]{1,31});`)

// htmlToText strips tags and decodes entities, keeping block boundaries as
// blank lines and <br> as a line break. Script and style content is
// dropped. The result still needs whitespace cleanup (plainLine/plainBlock).
func htmlToText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	// Markup that was itself entity-encoded ("&lt;p&gt;Mix&lt;/p&gt;")
	// decodes to real tags first. A literal "a &lt; b" becomes "a < b",
	// which the tokenizer still treats as text.
	if strings.Contains(s, "&lt;") {
		s = html.UnescapeString(s)
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0 // inside <script> or <style>
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch tag := string(name); {
			case tag == "script" || tag == "style":
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case tag == "br":
				b.WriteByte('\n')
			case blockTags[tag]:
				b.WriteString("\n\n")
			}
		}
	}
	out := b.String()
	if strings.Contains(out, "&") {
		out = entityRE.ReplaceAllStringFunc(out, html.UnescapeString)
	}
	return out
}

// plainLine reduces an HTML fragment to a single line of plain text.
func plainLine(s string) string { return collapse(htmlToText(s)) }

// plainBlock reduces an HTML fragment to plain text paragraphs separated by
// blank lines; single line breaks are kept.
func plainBlock(s string) string { return tidyBlock(htmlToText(s)) }

// collapse trims, collapses whitespace runs (newlines included) to single
// spaces, and drops control characters. It matches recipe.Normalize's
// cleanLine, so Normalize leaves our output unchanged.
func collapse(s string) string {
	return strings.Join(strings.FieldsFunc(stripControl(s), unicode.IsSpace), " ")
}

// tidyBlock collapses whitespace within each line and runs of blank lines
// to one blank line, and trims blank lines at both ends.
func tidyBlock(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var out []string
	blank := false
	for _, l := range strings.Split(s, "\n") {
		l = collapse(l)
		if l == "" {
			blank = len(out) > 0
			continue
		}
		if blank {
			out = append(out, "")
			blank = false
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// paragraphs splits block text into steps: on blank lines when there are
// any, otherwise on line breaks. One paragraph stays one step. Each piece is
// collapsed to a single line.
func paragraphs(block string) []string {
	var parts []string
	if strings.Contains(block, "\n\n") {
		parts = strings.Split(block, "\n\n")
	} else {
		parts = strings.Split(block, "\n")
	}
	var out []string
	for _, p := range parts {
		if p = collapse(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
	}, strings.ToValidUTF8(s, "\uFFFD"))
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) (string, bool) {
	i := 0
	for pos := range s {
		if i == n {
			return strings.TrimSpace(s[:pos]), true
		}
		i++
	}
	return s, false
}
