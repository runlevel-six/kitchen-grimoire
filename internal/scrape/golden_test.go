// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.json from current output")

// golden is the stable rendering of a Result compared against
// testdata/golden/<name>.json. RawSource is summarized by its top-level
// keys rather than stored byte for byte.
type golden struct {
	Error         string        `json:"error,omitempty"`
	Method        Method        `json:"method,omitempty"`
	CanonicalURL  string        `json:"canonicalUrl,omitempty"`
	ImageURLs     []string      `json:"imageUrls,omitempty"`
	RawSourceKeys []string      `json:"rawSourceKeys,omitempty"`
	Recipe        *goldenRecipe `json:"recipe,omitempty"`
}

type goldenRecipe struct {
	Title            string              `json:"title"`
	Description      string              `json:"description,omitempty"`
	YieldText        string              `json:"yieldText,omitempty"`
	Servings         float64             `json:"servings,omitempty"`
	PrepMin          int                 `json:"prepMin,omitempty"`
	CookMin          int                 `json:"cookMin,omitempty"`
	TotalMin         int                 `json:"totalMin,omitempty"`
	Author           string              `json:"author,omitempty"`
	SourceKind       recipe.SourceKind   `json:"sourceKind"`
	SourceURL        string              `json:"sourceUrl,omitempty"`
	SourceSite       string              `json:"sourceSite,omitempty"`
	Categories       [][]string          `json:"categories,omitempty"`
	Tags             []string            `json:"tags,omitempty"`
	IngredientGroups []goldenIngredients `json:"ingredientGroups,omitempty"`
	StepGroups       []recipe.StepGroup  `json:"stepGroups,omitempty"`
	NeedsReview      bool                `json:"needsReview,omitempty"`
	ReviewReasons    []string            `json:"reviewReasons,omitempty"`
}

type goldenIngredients struct {
	Name  string   `json:"name,omitempty"`
	Lines []string `json:"lines"`
}

func render(res *Result, err error) golden {
	if err != nil {
		return golden{Error: err.Error()}
	}
	r := res.Recipe
	g := golden{
		Method:       res.Method,
		CanonicalURL: res.CanonicalURL,
		ImageURLs:    res.ImageURLs,
		Recipe: &goldenRecipe{
			Title: r.Title, Description: r.Description, YieldText: r.YieldText, Servings: r.Servings,
			PrepMin: r.PrepMin, CookMin: r.CookMin, TotalMin: r.TotalMin, Author: r.Author,
			SourceKind: r.SourceKind, SourceURL: r.SourceURL, SourceSite: r.SourceSite,
			Categories: r.Categories, Tags: r.Tags, StepGroups: r.StepGroups,
			NeedsReview: r.NeedsReview, ReviewReasons: r.ReviewReasons,
		},
	}
	for _, ig := range r.IngredientGroups {
		gi := goldenIngredients{Name: ig.Name}
		for _, in := range ig.Ingredients {
			gi.Lines = append(gi.Lines, in.Raw)
		}
		g.Recipe.IngredientGroups = append(g.Recipe.IngredientGroups, gi)
	}
	if len(res.RawSource) > 0 {
		var m map[string]any
		if json.Unmarshal(res.RawSource, &m) == nil {
			for k := range m {
				g.RawSourceKeys = append(g.RawSourceKeys, k)
			}
			sort.Strings(g.RawSourceKeys)
		}
	}
	return g
}

// fixtureURL is the page URL every golden fixture is "fetched" from: www
// to exercise SourceSite, and a tracking parameter and fragment to
// exercise CanonicalURL when the page has no canonical link.
func fixtureURL(name string) *url.URL {
	u, _ := url.Parse("https://www.recipes.example.com/recipes/" + name + "/?utm_source=newsletter#top")
	return u
}

func TestGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "golden", "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".html")
		t.Run(name, func(t *testing.T) {
			page, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Extract(page, fixtureURL(name))
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			if err := enc.Encode(render(res, err)); err != nil {
				t.Fatal(err)
			}
			got := buf.Bytes()

			want := strings.TrimSuffix(file, ".html") + ".json"
			if *update {
				if err := os.WriteFile(want, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			exp, err := os.ReadFile(want)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if !bytes.Equal(got, exp) {
				t.Errorf("output differs from %s:\n--- got\n%s\n--- want\n%s", want, got, exp)
			}
			if res != nil {
				checkDraft(t, res)
			}
		})
	}
}

// checkDraft asserts a draft passes recipe.Normalize and recipe.Validate,
// and that Normalize had nothing to remove (the extractor already emits
// clean text).
func checkDraft(t *testing.T, res *Result) {
	t.Helper()
	r := *res.Recipe
	steps, lines := r.StepCount(), r.IngredientCount()
	title := r.Title
	recipe.Normalize(&r)
	if err := recipe.Validate(&r); err != nil {
		t.Errorf("draft fails validation: %v", err)
	}
	if r.StepCount() != steps || r.IngredientCount() != lines || r.Title != title {
		t.Errorf("Normalize changed the draft: steps %d→%d, lines %d→%d, title %q→%q",
			steps, r.StepCount(), lines, r.IngredientCount(), title, r.Title)
	}
	if res.Method != MethodOpenGraph && !json.Valid(res.RawSource) {
		t.Errorf("RawSource is not valid JSON: %s", res.RawSource)
	}
	for _, u := range res.ImageURLs {
		pu, err := url.Parse(u)
		if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			t.Errorf("bad image URL %q", u)
		}
	}
}

func TestNoRecipe(t *testing.T) {
	for _, page := range []string{"", "<html></html>", "not html at all", `<script type="application/ld+json">{"@type":"Thing"}</script>`} {
		if _, err := Extract([]byte(page), fixtureURL("x")); !errors.Is(err, ErrNoRecipe) {
			t.Errorf("Extract(%q) error = %v, want ErrNoRecipe", page, err)
		}
	}
}

// TestRawSourceInlinesRefs checks that @id references in the Recipe are
// replaced by the nodes they point at, so raw_source stands alone.
func TestRawSourceInlinesRefs(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "golden", "graph-idrefs.html"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Extract(page, fixtureURL("graph-idrefs"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Author   map[string]any `json:"author"`
		Image    map[string]any `json:"image"`
		IsPartOf map[string]any `json:"isPartOf"`
	}
	if err := json.Unmarshal(res.RawSource, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Author["name"] != "Example Test Cook" || raw.Image["width"] != float64(1200) {
		t.Errorf("references not inlined: %s", res.RawSource)
	}
	if len(raw.IsPartOf) != 1 {
		t.Errorf("isPartOf should stay a reference: %v", raw.IsPartOf)
	}
}

// TestLocalFixtures runs Extract over real page captures in
// $GRIMOIRE_LOCAL_FIXTURES (make test-local). They are never committed
// (§17), so this only checks that nothing panics and the drafts are sane.
func TestLocalFixtures(t *testing.T) {
	dir := os.Getenv("GRIMOIRE_LOCAL_FIXTURES")
	if dir == "" {
		t.Skip("GRIMOIRE_LOCAL_FIXTURES not set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skipf("no *.html in %s", dir)
	}
	counts := map[Method]int{}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			page, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse("https://local.example/" + filepath.Base(file))
			res, err := Extract(page, u)
			if errors.Is(err, ErrNoRecipe) {
				counts["none"]++
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			counts[res.Method]++
			checkDraft(t, res)
			t.Logf("%s: %q, %d ingredients, %d steps, %d images, reasons %v",
				res.Method, res.Recipe.Title, res.Recipe.IngredientCount(), res.Recipe.StepCount(),
				len(res.ImageURLs), res.Recipe.ReviewReasons)
		})
	}
	t.Logf("methods: %v", counts)
}
