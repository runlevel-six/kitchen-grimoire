// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"slices"
	"sort"
	"strings"
	"unicode"
)

// RenameThreshold is the title similarity at or above which two titles
// from different editions are taken as the same recipe renamed (D-13).
// Similarity is the share of title words that pair up (see
// TitleSimilarity). 0.85 accepts one changed word in four or more
// ("CHOCOLATE CHIP OATMEAL COOKIES" vs "CHOCOLATE CHIP COOKIES") but not
// one in three ("CHOCOLATE CAKE" vs "CHOCOLATE CHIP CAKE", 0.8), which is
// too often a different recipe.
const RenameThreshold = 0.85

// stopWords carry no identity in a title.
var stopWords = map[string]bool{"a": true, "an": true, "the": true, "and": true, "with": true, "of": true}

// NormalizeTitle folds a title for exact matching: lower-case, "&" →
// "and", apostrophes dropped ("MOM'S" → "moms"), other punctuation as
// spaces, single spaces.
func NormalizeTitle(s string) string {
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// titleWords are a normalized title's words without stop words, with a
// plural "s"/"es" removed.
func titleWords(s string) []string {
	var out []string
	for _, w := range strings.Fields(NormalizeTitle(s)) {
		if stopWords[w] {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

func stem(w string) string {
	switch {
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y" // "berries" → "berry", "cookies" → "cooky"
	case len(w) > 4 && strings.HasSuffix(w, "ie"):
		return w[:len(w)-2] + "y" // "cookie" → "cooky", to meet its plural
	case len(w) > 4 && (strings.HasSuffix(w, "ches") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "oes")):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}

// wordsMatch pairs two title words: equal, an abbreviation (a prefix of at
// least 3 letters: "choc" ~ "chocolate"), or one typo in a long word
// ("chilli" ~ "chili").
func wordsMatch(a, b string) bool {
	if a == b {
		return true
	}
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	if len(short) >= 3 && strings.HasPrefix(long, short) && !isNumber(short) {
		return true
	}
	return len(short) >= 5 && editDistanceAtMost1(a, b)
}

func isNumber(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) }) < 0
}

// editDistanceAtMost1 reports whether a and b differ by at most one
// insertion, deletion, or substitution.
func editDistanceAtMost1(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	if len(rb)-len(ra) > 1 {
		return false
	}
	i, j, diff := 0, 0, 0
	for i < len(ra) && j < len(rb) {
		if ra[i] == rb[j] {
			i++
			j++
			continue
		}
		diff++
		if diff > 1 {
			return false
		}
		if len(ra) == len(rb) {
			i++
		}
		j++
	}
	return diff+(len(rb)-j)+(len(ra)-i) <= 1
}

// TitleSimilarity scores two titles from 0 to 1. Titles equal once
// normalized, or once spaces are removed ("CORN BREAD" ~ "CORNBREAD"),
// score 1. Otherwise it is the Dice coefficient over title words, where
// words pair up as in wordsMatch and two adjacent words may pair with
// their concatenation ("POPPY SEED CAKE" ~ "POPPYSEED CAKE").
func TitleSimilarity(a, b string) float64 {
	na, nb := NormalizeTitle(a), NormalizeTitle(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb || strings.ReplaceAll(na, " ", "") == strings.ReplaceAll(nb, " ", "") {
		return 1
	}
	wa, wb := titleWords(a), titleWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	wa, wb = joinCompounds(wa, wb), joinCompounds(wb, wa)
	used := make([]bool, len(wb))
	matched := 0
	for _, x := range wa {
		for j, y := range wb {
			if !used[j] && wordsMatch(x, y) {
				used[j] = true
				matched++
				break
			}
		}
	}
	return 2 * float64(matched) / float64(len(wa)+len(wb))
}

// joinCompounds merges adjacent words of w whose concatenation is a word
// of other (or abbreviates to one), so "poppy seed" lines up with
// "poppyseed".
func joinCompounds(w, other []string) []string {
	var out []string
	for i := 0; i < len(w); i++ {
		if i+1 < len(w) && isWordOf(w[i]+w[i+1], other) {
			out = append(out, stem(w[i]+w[i+1]))
			i++
			continue
		}
		out = append(out, w[i])
	}
	return out
}

func isWordOf(w string, words []string) bool {
	for _, o := range words {
		if w == o || stem(w) == o {
			return true
		}
	}
	return false
}

// Merge combines two editions of a collection (D-13). primary is the
// newer edition and wins; older drafts are matched to it by normalized
// title, then by TitleSimilarity at or above RenameThreshold (one-to-one,
// best scores first). A matched primary draft gets the older draft in
// Older, and a fuzzy match is flagged title_changed_between_editions.
// Older drafts with no match are kept, flagged only_in_older_edition, and
// placed after the primary draft matched to the nearest preceding older
// recipe, so the merged list follows the source order.
func Merge(primary, older []Draft) []Draft {
	// Work on copies: flags are appended to the review reasons, which
	// must not alias the callers' slices.
	out := make([]Draft, len(primary))
	for i, d := range primary {
		d.Recipe.ReviewReasons = slices.Clone(d.Recipe.ReviewReasons)
		out[i] = d
	}
	older = slices.Clone(older)
	for i := range older {
		older[i].Recipe.ReviewReasons = slices.Clone(older[i].Recipe.ReviewReasons)
	}
	olderTo := make([]int, len(older)) // index into out, or -1
	for i := range olderTo {
		olderTo[i] = -1
	}
	taken := make([]bool, len(out))

	// 1. Exact matches on the normalized title, in source order.
	byTitle := map[string][]int{}
	for i, d := range out {
		k := NormalizeTitle(d.Recipe.Title)
		byTitle[k] = append(byTitle[k], i)
	}
	for oi, od := range older {
		for _, pi := range byTitle[NormalizeTitle(od.Recipe.Title)] {
			if !taken[pi] {
				taken[pi], olderTo[oi] = true, pi
				break
			}
		}
	}

	// 2. Likely renames among the rest, best score first; ties go to the
	// pair whose positions in their sources are closest.
	type cand struct {
		oi, pi int
		score  float64
	}
	var cands []cand
	for oi, od := range older {
		if olderTo[oi] >= 0 {
			continue
		}
		for pi, pd := range out {
			if taken[pi] {
				continue
			}
			if s := TitleSimilarity(od.Recipe.Title, pd.Recipe.Title); s >= RenameThreshold {
				cands = append(cands, cand{oi, pi, s})
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return absInt(cands[i].oi-cands[i].pi) < absInt(cands[j].oi-cands[j].pi)
	})
	fuzzy := map[int]float64{}
	for _, c := range cands {
		if olderTo[c.oi] >= 0 || taken[c.pi] {
			continue
		}
		taken[c.pi], olderTo[c.oi] = true, c.pi
		fuzzy[c.pi] = c.score
	}

	// 3. Attach the matches.
	for oi, pi := range olderTo {
		if pi < 0 {
			continue
		}
		od := older[oi]
		out[pi].Older = &od
		out[pi].MatchScore = 1
		if s, ok := fuzzy[pi]; ok {
			out[pi].MatchScore = s
			out[pi].flag(ReasonTitleChanged)
		}
	}

	// 4. Insert the older-only drafts.
	after := map[int][]Draft{} // primary index → older-only drafts after it; -1 = before all
	prev := -1
	for oi, od := range older {
		if pi := olderTo[oi]; pi >= 0 {
			prev = pi
			continue
		}
		od.OlderOnly = true
		od.flag(ReasonOlderOnly)
		after[prev] = append(after[prev], od)
	}
	merged := make([]Draft, 0, len(out)+len(older))
	merged = append(merged, after[-1]...)
	for i, d := range out {
		merged = append(merged, d)
		merged = append(merged, after[i]...)
	}
	return merged
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
