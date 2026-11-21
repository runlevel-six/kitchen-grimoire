// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Class is what a title-line field means (docs/DESIGN.md §8.4 step 4).
type Class string

const (
	ClassCategory        Class = "category"
	ClassSubcategory     Class = "subcategory"
	ClassContributor     Class = "contributor"
	ClassPublishedSource Class = "published_source"
	ClassTag             Class = "tag"
	ClassNote            Class = "note"
	ClassGroupLabel      Class = "group_label"
	ClassCompound        Class = "compound"
)

func (c Class) valid() bool {
	switch c {
	case ClassCategory, ClassSubcategory, ClassContributor, ClassPublishedSource,
		ClassTag, ClassNote, ClassGroupLabel, ClassCompound:
		return true
	}
	return false
}

// Assignment is one meaning of a token: a class and the canonical value.
// A plain token has one; a compound token has several.
type Assignment struct {
	Class Class
	Value string
}

// Entry is one row of the mapping file.
type Entry struct {
	Token     string // as written in the map
	Class     Class
	Canonical string // the merged spelling; defaults to Token
	Comment   string
	Parts     []Assignment // what the token means; one element unless compound
}

// Map is the attribution mapping file: token → meaning. Lookups are
// case-insensitive and ignore runs of whitespace and curly apostrophes.
type Map struct {
	entries    map[string]*Entry
	maxWords   int  // the longest token in words, for greedy matching
	categories bool // some entry is a category (see isCategory)
}

// NewMap returns an empty map (every token unknown).
func NewMap() *Map { return &Map{entries: map[string]*Entry{}} }

// LoadMap reads a mapping CSV with columns token,class,canonical,comment.
// A header row (first cell "token") and lines starting with '#' are
// skipped. Compound canonicals look like "tag=Microwave; contributor=Aunt
// Example". A token listed twice is an error, so the map stays unambiguous.
func LoadMap(r io.Reader) (*Map, error) {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	m := NewMap()
	first := true
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("mapping file: %w", err)
		}
		line, _ := cr.FieldPos(0)
		if first {
			first = false
			if strings.EqualFold(strings.TrimSpace(rec[0]), "token") {
				continue
			}
		}
		for len(rec) < 4 {
			rec = append(rec, "")
		}
		e := &Entry{
			Token:     strings.TrimSpace(rec[0]),
			Class:     Class(strings.ToLower(strings.TrimSpace(rec[1]))),
			Canonical: strings.TrimSpace(rec[2]),
			Comment:   strings.TrimSpace(rec[3]),
		}
		if e.Token == "" {
			continue
		}
		if !e.Class.valid() {
			return nil, fmt.Errorf("mapping file line %d: unknown class %q for token %q", line, rec[1], e.Token)
		}
		if e.Canonical == "" {
			e.Canonical = e.Token
		}
		if e.Class == ClassCompound {
			parts, err := parseCompound(e.Canonical)
			if err != nil {
				return nil, fmt.Errorf("mapping file line %d: token %q: %w", line, e.Token, err)
			}
			e.Parts = parts
		} else {
			e.Parts = []Assignment{{e.Class, e.Canonical}}
		}
		if err := m.add(e); err != nil {
			return nil, fmt.Errorf("mapping file line %d: %w", line, err)
		}
	}
	return m, nil
}

// add inserts an entry, rejecting duplicates.
func (m *Map) add(e *Entry) error {
	k := tokenKey(e.Token)
	if k == "" {
		return nil
	}
	if old, ok := m.entries[k]; ok {
		return fmt.Errorf("token %q is listed twice (also as %q)", e.Token, old.Token)
	}
	m.entries[k] = e
	m.categories = m.categories || e.isCategory()
	if n := len(strings.Fields(k)); n > m.maxWords {
		m.maxWords = n
	}
	return nil
}

// parseCompound reads "class=value; class=value".
func parseCompound(s string) ([]Assignment, error) {
	var out []Assignment
	for part := range strings.SplitSeq(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		c, v, ok := strings.Cut(part, "=")
		cls := Class(strings.ToLower(strings.TrimSpace(c)))
		v = strings.TrimSpace(v)
		if !ok || v == "" || !cls.valid() || cls == ClassCompound {
			return nil, fmt.Errorf("compound canonical %q: want \"class=value; class=value\"", s)
		}
		out = append(out, Assignment{cls, v})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("compound canonical is empty")
	}
	return out, nil
}

// Lookup returns the entry for a token, or nil if it is unknown.
func (m *Map) Lookup(token string) *Entry {
	if m == nil {
		return nil
	}
	return m.entries[tokenKey(token)]
}

// isCategory reports whether the entry starts a recipe's category: a
// category token, or a compound that includes one.
func (e *Entry) isCategory() bool {
	if e == nil {
		return false
	}
	for _, p := range e.Parts {
		if p.Class == ClassCategory {
			return true
		}
	}
	return false
}

// hasCategories reports whether the map knows any category, which strict
// title detection needs.
func (m *Map) hasCategories() bool {
	return m != nil && m.categories
}

// tokenKey is the lookup key: lower-case, single-spaced, straight
// apostrophes, and surrounding field punctuation removed.
func tokenKey(s string) string {
	s = strings.ReplaceAll(s, "’", "'")
	s = strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';'
	})
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// match is one token found in a run of text by matchPrefix/splitWords.
type match struct {
	Text  string // as it appears in the source
	Entry *Entry // nil for leftover text
}

// matchPrefix finds the longest known token at the start of words. It
// returns the number of words it covers and the entry, or 0, nil.
func (m *Map) matchPrefix(words []string) (int, *Entry) {
	if m == nil {
		return 0, nil
	}
	for n := min(m.maxWords, len(words)); n >= 1; n-- {
		if e := m.entries[tokenKey(strings.Join(words[:n], " "))]; e != nil {
			return n, e
		}
	}
	return 0, nil
}

// splitWords covers a run of space-separated text greedily with the
// longest known tokens, left to right. Text no token covers is returned in
// order as leftover runs (Entry == nil), each as one match.
func (m *Map) splitWords(s string) []match {
	words := strings.Fields(s)
	var out []match
	var left []string
	flush := func() {
		if len(left) > 0 {
			out = append(out, match{Text: strings.Join(left, " ")})
			left = nil
		}
	}
	for i := 0; i < len(words); {
		if n, e := m.matchPrefix(words[i:]); n > 0 {
			flush()
			out = append(out, match{Text: strings.Join(words[i:i+n], " "), Entry: e})
			i += n
			continue
		}
		left = append(left, words[i])
		i++
	}
	flush()
	return out
}
