// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
)

// TokenCount is one distinct title-line token and how often it occurs.
type TokenCount struct {
	Token     string // first spelling seen
	Count     int
	Class     string // the map's class, or "UNKNOWN"
	Canonical string
}

// Tokens lists every distinct field token on the title lines of text, for
// filling in the mapping file (--emit-tokens). It uses the loose title
// rules, so titles whose category the map doesn't know yet are found too.
// Text no known token covers is listed as one token per run.
func Tokens(text string, format Format, m *Map) []TokenCount {
	if m == nil {
		m = NewMap()
	}
	doc := split(text, format, m, true)
	byKey := map[string]*TokenCount{}
	var order []string
	for _, b := range doc.Blocks {
		toks, _ := fieldTokens(b.Fields, m)
		for _, t := range toks {
			k := tokenKey(t.Text)
			tc, ok := byKey[k]
			if !ok {
				tc = &TokenCount{Token: t.Text, Class: "UNKNOWN"}
				if t.Entry != nil {
					tc.Class, tc.Canonical = string(t.Entry.Class), t.Entry.Canonical
				}
				byKey[k] = tc
				order = append(order, k)
			}
			tc.Count++
		}
	}
	out := make([]TokenCount, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return tokenKey(out[i].Token) < tokenKey(out[j].Token)
	})
	return out
}

// WriteTokensCSV writes token counts as CSV: token,count,class,canonical.
func WriteTokensCSV(w io.Writer, toks []TokenCount) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"token", "count", "class", "canonical"})
	for _, t := range toks {
		_ = cw.Write([]string{t.Token, strconv.Itoa(t.Count), t.Class, t.Canonical})
	}
	cw.Flush()
	return cw.Error()
}
