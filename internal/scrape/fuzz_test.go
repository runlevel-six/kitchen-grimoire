// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// FuzzExtract feeds arbitrary bytes as a page (§15.2). Extract must not
// panic, must fail only with ErrNoRecipe, and every draft it returns must
// pass recipe.Normalize and recipe.Validate.
func FuzzExtract(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "golden", "*.html"))
	for _, file := range files {
		if b, err := os.ReadFile(file); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(`<script type="application/ld+json">{"@type":"Recipe","name":"x","recipeInstructions":{"@id":"a"},"@id":"a"}</script>`))
	f.Add([]byte(`<div itemscope itemtype="https://schema.org/Recipe"><div itemprop="recipeInstructions" itemscope itemtype="https://schema.org/HowToSection"></div></div>`))
	u := fixtureURL("fuzz")
	f.Fuzz(func(t *testing.T, page []byte) {
		res, err := Extract(page, u)
		if err != nil {
			if !errors.Is(err, ErrNoRecipe) {
				t.Fatalf("unexpected error: %v", err)
			}
			return
		}
		r := *res.Recipe
		recipe.Normalize(&r)
		if err := recipe.Validate(&r); err != nil {
			t.Fatalf("draft fails validation: %v", err)
		}
	})
}

// FuzzParseISODuration checks the duration parser never panics and stays
// within its documented range.
func FuzzParseISODuration(f *testing.F) {
	for _, s := range []string{"PT1H15M", "PT90M", "P0DT1H", "PT1H 15M", "pt45m", "PT1.5H", "45 minutes", "1:15", "30", "P1Y2M", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, ok := ParseISODuration(s)
		if !ok {
			if d != 0 {
				t.Fatalf("ParseISODuration(%q) = %v with ok=false", s, d)
			}
			return
		}
		if d < 0 || d > maxDuration+time.Second {
			t.Fatalf("ParseISODuration(%q) = %v out of range", s, d)
		}
	})
}
