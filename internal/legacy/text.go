// SPDX-License-Identifier: Apache-2.0

// Package legacy converts a line-structured legacy recipe document (UTF-8
// text, D-12) into recipe drafts for bulk import (docs/DESIGN.md §8.4).
//
// The pipeline is: read lines → split into blocks at title lines → split
// the title fields with the mapping file → separate ingredients from
// instructions → normalize legacy ingredient naming → flag anything
// uncertain for review. Every block keeps its verbatim text, so nothing in
// the source is lost even when a heuristic guesses wrong.
package legacy

import (
	"fmt"
	"regexp"
	"strings"
)

// Format is the flavour of text the source was converted to.
type Format int

const (
	// FormatAuto picks Tabbed or PDFText by looking at the text.
	FormatAuto Format = iota
	// FormatTabbed is word-processor text (textutil, LibreOffice): title
	// fields are separated by tabs and every paragraph is one line.
	FormatTabbed
	// FormatPDFText is text extracted from a PDF (scripts/pdf2txt.js):
	// runs of spaces are collapsed, paragraphs are wrapped at the page
	// width, and pages start with a form feed and a "[[PAGE n]]" line.
	FormatPDFText
)

func (f Format) String() string {
	switch f {
	case FormatTabbed:
		return "tabbed"
	case FormatPDFText:
		return "pdftext"
	}
	return "auto"
}

// ParseFormat reads a --format flag value.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return FormatAuto, nil
	case "tabbed", "tab", "txt", "doc":
		return FormatTabbed, nil
	case "pdftext", "pdf":
		return FormatPDFText, nil
	}
	return FormatAuto, fmt.Errorf("unknown format %q (want tabbed, pdftext, or auto)", s)
}

// DetectFormat guesses the flavour: page markers mean PDF text; otherwise
// tabs mean word-processor text; with neither, the fields must have been
// collapsed, so it is treated as PDF text.
func DetectFormat(text string) Format {
	tabs := false
	for _, l := range scanLines(text) {
		// Only a "[[PAGE n]]" marker counts: Word text has bare form feeds too.
		if l.marker && pageMarker.MatchString(l.text) {
			return FormatPDFText
		}
		if strings.Contains(l.text, "\t") {
			tabs = true
		}
	}
	if tabs {
		return FormatTabbed
	}
	return FormatPDFText
}

// srcLine is one line of the input with its byte span. text has the line
// terminator, form feeds, and a trailing '\r' removed; [start, end)
// includes the terminator, so consecutive spans tile the input exactly.
type srcLine struct {
	text       string
	start, end int
	marker     bool // a page marker ("[[PAGE n]]"), a bare form feed, or a Word field code: not content
}

// pageMarker matches pdf2txt.js page markers once form feeds are removed.
var pageMarker = regexp.MustCompile(`^\s*\[\[PAGE \d+\]\]\s*$`)

// wordField matches Word field codes that text conversion leaves behind,
// most importantly a table of contents: `TOC \o "1-3"` and one
// `TITLE<TAB>Category<TAB> PAGEREF _Toc12345 \h 7` line per entry. TOC
// lines look exactly like title lines, so they must never be content.
var wordField = regexp.MustCompile(`\bPAGEREF\s+_Toc\d+|^\s*TOC\s+\\[a-z]\b`)

// scanLines splits text at "\n", "\r\n", or a lone "\r" (old Mac line
// ends, which some converters still write).
func scanLines(text string) []srcLine {
	var out []srcLine
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c != '\n' && c != '\r' {
			continue
		}
		end := i + 1
		if c == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			end++
		}
		out = append(out, makeLine(text, start, i, end))
		start = end
		i = end - 1
	}
	if start < len(text) {
		out = append(out, makeLine(text, start, len(text), len(text)))
	}
	return out
}

func makeLine(text string, start, contentEnd, end int) srcLine {
	raw := text[start:contentEnd]
	hadFF := strings.Contains(raw, "\f")
	t := strings.ReplaceAll(raw, "\f", "")
	l := srcLine{text: t, start: start, end: end}
	// A form feed alone is a page break (Word); a marker line is PDF text.
	l.marker = pageMarker.MatchString(t) || (hadFF && strings.TrimSpace(t) == "") || wordField.MatchString(t)
	return l
}

// visibleText is a block's text without page markers and form feeds, with
// "\n" line ends and trailing blank lines removed. It becomes OriginalText.
func visibleText(lines []srcLine) string {
	var b strings.Builder
	for _, l := range lines {
		if l.marker {
			continue
		}
		b.WriteString(strings.TrimRight(l.text, " \t"))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), " \t\n")
}
