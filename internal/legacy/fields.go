// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"strings"
)

// fieldTokens splits a title line's field text into tokens. Tab-separated
// fields are split at the tabs first; each field that is not itself a
// known token is then covered greedily by the longest known tokens
// (case-insensitive, whole words). Leftover text comes back with a nil
// Entry. tabbed reports whether the fields were tab-separated, which
// decides how leftovers are reported (an unknown token vs unsplit text).
func fieldTokens(fields string, m *Map) (toks []match, tabbed bool) {
	tabbed = strings.Contains(fields, "\t")
	for f := range strings.SplitSeq(fields, "\t") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if e := m.Lookup(f); e != nil {
			toks = append(toks, match{Text: f, Entry: e})
			continue
		}
		toks = append(toks, m.splitWords(f)...)
	}
	return toks, tabbed
}

// attribution is what a title line's fields say about a recipe.
type attribution struct {
	categories   [][]string
	tags         []string
	contributors []string
	authors      []string
	notes        []string
	reasons      []string
}

// classify turns field tokens into recipe attributes and review reasons.
// A category starts a new category path; a subcategory extends the most
// recent one.
func classify(toks []match, tabbed bool) attribution {
	var a attribution
	addOnce := func(list *[]string, v string) {
		for _, x := range *list {
			if strings.EqualFold(x, v) {
				return
			}
		}
		*list = append(*list, v)
	}
	for _, t := range toks {
		if t.Entry == nil {
			if tabbed {
				a.reasons = append(a.reasons, "unclassified_token:"+t.Text)
			} else {
				a.reasons = append(a.reasons, "unsplit_fields:"+t.Text)
			}
			continue
		}
		for _, p := range t.Entry.Parts {
			switch p.Class {
			case ClassCategory:
				a.categories = append(a.categories, []string{p.Value})
			case ClassSubcategory:
				if len(a.categories) == 0 {
					a.categories = append(a.categories, []string{p.Value})
					a.reasons = append(a.reasons, "subcategory_without_category:"+p.Value)
				} else {
					last := &a.categories[len(a.categories)-1]
					*last = append(*last, p.Value)
				}
			case ClassTag:
				addOnce(&a.tags, p.Value)
			case ClassContributor:
				addOnce(&a.contributors, p.Value)
			case ClassPublishedSource:
				addOnce(&a.authors, p.Value)
			case ClassNote, ClassGroupLabel:
				addOnce(&a.notes, p.Value)
			}
		}
	}
	return a
}
