// SPDX-License-Identifier: Apache-2.0

package recipe

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// MaxSlug caps slug length so URLs stay readable.
const MaxSlug = 80

// Slugify turns a title into a URL slug: lower-case ASCII letters and
// digits separated by single hyphens. Accents are folded ("Crème" → "creme");
// "&" becomes "and". Other characters separate words.
func Slugify(s string) string {
	s = strings.ReplaceAll(s, "&", " and ")
	// NFD splits "é" into "e" + a combining accent, which we then drop.
	s = norm.NFD.String(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r): // combining mark: drop
			continue
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(unicode.ToLower(r))
			dash = false
		case r == '\'' || r == '’':
			// "Mom's" → "moms", not "mom-s"
			continue
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if len(out) > MaxSlug {
		out = strings.TrimSuffix(out[:MaxSlug], "-")
		if i := strings.LastIndexByte(out, '-'); i > MaxSlug/2 {
			out = out[:i] // cut at a word boundary when it's not too short
		}
	}
	return out
}

// CategoryPathSlug is the unique key for a category path:
// ["Desserts", "Cookies"] → "desserts/cookies".
func CategoryPathSlug(path []string) string {
	parts := make([]string, 0, len(path))
	for _, p := range path {
		parts = append(parts, Slugify(p))
	}
	return strings.Join(parts, "/")
}
