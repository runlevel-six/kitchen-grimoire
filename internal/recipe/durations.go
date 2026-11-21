// SPDX-License-Identifier: Apache-2.0

package recipe

import (
	"regexp"
	"strconv"
	"strings"
)

// Span is a piece of step text. If Seconds > 0 the span is a duration that
// cook mode can turn into a timer (docs/DESIGN.md §9.3).
type Span struct {
	Text    string
	Seconds int
}

// durationRE matches "20 minutes", "15-20 min", "1 to 2 hours", "1½ hours",
// "45 seconds". Group 1 is the first number, group 2 the optional upper
// bound, group 3 the unit word.
var durationRE = regexp.MustCompile(
	`(?i)\b(\d+(?:\.\d+)?[½¼¾]?|[½¼¾])(?:\s*(?:-|–|to)\s*(\d+(?:\.\d+)?[½¼¾]?))?\s*` +
		`(hours?|hrs?|minutes?|mins?|seconds?|secs?)\b`)

var fractions = map[rune]float64{'½': 0.5, '¼': 0.25, '¾': 0.75}

// parseNum handles "20", "1.5", "1½", "½".
func parseNum(s string) (float64, bool) {
	var frac float64
	for r, v := range fractions {
		if strings.HasSuffix(s, string(r)) {
			frac = v
			s = strings.TrimSuffix(s, string(r))
			break
		}
	}
	if s == "" {
		return frac, frac > 0
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return n + frac, true
}

// SplitDurations splits step text into plain and duration spans. For a
// range, the timer uses the upper bound. "Overnight" is not a timer.
func SplitDurations(text string) []Span {
	var out []Span
	last := 0
	for _, m := range durationRE.FindAllStringSubmatchIndex(text, -1) {
		lo, ok := parseNum(text[m[2]:m[3]])
		if !ok {
			continue
		}
		val := lo
		if m[4] >= 0 {
			if hi, ok := parseNum(text[m[4]:m[5]]); ok {
				val = hi
			}
		}
		unit := strings.ToLower(text[m[6]:m[7]])
		var mult float64
		switch {
		case strings.HasPrefix(unit, "h"):
			mult = 3600
		case strings.HasPrefix(unit, "m"):
			mult = 60
		default:
			mult = 1
		}
		secs := int(val * mult)
		if secs <= 0 {
			continue
		}
		if m[0] > last {
			out = append(out, Span{Text: text[last:m[0]]})
		}
		out = append(out, Span{Text: text[m[0]:m[1]], Seconds: secs})
		last = m[1]
	}
	if last < len(text) {
		out = append(out, Span{Text: text[last:]})
	}
	return out
}
