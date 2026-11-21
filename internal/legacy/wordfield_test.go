// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"strings"
	"testing"
)

// A Word table of contents survives text conversion as field-code lines
// that look exactly like recipe title lines. They must produce no recipes.
func TestWordTOCIsIgnored(t *testing.T) {
	m := testMap(t)
	text := " TOC \\o \"1-3\" EXAMPLE CHEESE BALL\tDesserts\t PAGEREF _Toc75871145 \\h 1\n" +
		"EXAMPLE DIP\tDesserts\t PAGEREF _Toc75871146 \\h 2\n" +
		"\f\n" +
		"EXAMPLE CHEESE BALL\tDesserts\n1 8-oz pkg cream cheese\nMix well and chill.\n"
	res := Convert(text, Options{Format: FormatTabbed, Map: m, Collection: "Example"})
	if len(res.Drafts) != 1 || !strings.Contains(res.Drafts[0].Recipe.Title, "CHEESE BALL") {
		var titles []string
		for _, d := range res.Drafts {
			titles = append(titles, d.Recipe.Title)
		}
		t.Fatalf("drafts = %q, want only the real recipe", titles)
	}
}
