// SPDX-License-Identifier: Apache-2.0

package ingredient

import "strings"

// Line is one ingredient line. Raw is canonical and always kept verbatim;
// the parsed fields are derived from it and may be recomputed (D-06).
// ParseOK is false when the line didn't match the grammar in §9.1; then
// only Raw is meaningful and the line is never scaled.
type Line struct {
	Raw string `json:"raw"`

	ParseOK bool    `json:"parseOk,omitempty"`
	QtyMin  float64 `json:"qtyMin,omitempty"`
	QtyMax  float64 `json:"qtyMax,omitempty"` // equal to QtyMin unless a range ("3-4")
	Unit    string  `json:"unit,omitempty"`
	Size    string  `json:"size,omitempty"` // "8-oz" in "1 8-oz pkg cream cheese"; never scaled
	Item    string  `json:"item,omitempty"`
	Note    string  `json:"note,omitempty"` // after the first ", "
}

// Parse parses one ingredient line (§9.1). Until P1.3 lands it only trims
// the line and marks it unparsed.
func Parse(raw string) Line {
	return Line{Raw: strings.TrimSpace(raw)}
}
