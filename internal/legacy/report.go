// SPDX-License-Identifier: Apache-2.0

package legacy

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// draftLine is one line of drafts.jsonl: the recipe, plus the older
// edition's version when the editions were matched.
type draftLine struct {
	recipe.Recipe
	Older *recipe.Recipe `json:"older,omitempty"`
}

// WriteDraftsJSONL writes one JSON recipe per line.
func WriteDraftsJSONL(w io.Writer, drafts []Draft) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	for _, d := range drafts {
		l := draftLine{Recipe: d.Recipe}
		if d.Older != nil {
			l.Older = &d.Older.Recipe
		}
		if err := enc.Encode(l); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// Report is everything the review reports describe.
type Report struct {
	Collection       string
	Format           Format
	PrefixLines      int
	HasOlder         bool
	OlderFormat      Format
	OlderPrefixLines int
	Drafts           []Draft
	// TitleChanges lists import keys whose ordinal now holds a different
	// title than in a previous run (re-running would create duplicates).
	TitleChanges []string
}

// edition says which editions a draft came from.
func (r *Report) edition(d Draft) string {
	switch {
	case !r.HasOlder:
		return ""
	case d.OlderOnly:
		return "older_only"
	case d.Older != nil:
		return "both"
	}
	return "newer_only"
}

// CategoryPath renders a recipe's categories: "Desserts > Cookies; Breads".
func CategoryPath(r recipe.Recipe) string {
	var paths []string
	for _, p := range r.Categories {
		paths = append(paths, strings.Join(p, " > "))
	}
	return strings.Join(paths, "; ")
}

// WriteCSV writes report.csv: one row per draft.
func (r *Report) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"ordinal", "edition", "import_key", "title", "category_path", "contributor",
		"author", "tags", "ingredients", "steps", "needs_review", "flags", "older_title", "match_score"})
	for _, d := range r.Drafts {
		rc := d.Recipe
		olderTitle, score := "", ""
		if d.Older != nil {
			olderTitle = d.Older.Recipe.Title
			score = strconv.FormatFloat(d.MatchScore, 'f', 2, 64)
		}
		_ = cw.Write([]string{
			strconv.Itoa(d.Ordinal), r.edition(d), rc.ImportKey, rc.Title, CategoryPath(rc),
			rc.ContributedBy, rc.Author, strings.Join(rc.Tags, "; "),
			strconv.Itoa(rc.IngredientCount()), strconv.Itoa(rc.StepCount()),
			strconv.FormatBool(rc.NeedsReview), strings.Join(rc.ReviewReasons, "; "),
			olderTitle, score,
		})
	}
	cw.Flush()
	return cw.Error()
}

// counted is a name and a count, for sorted report tables.
type counted struct {
	name string
	n    int
}

func sortCounts(m map[string]int) []counted {
	out := make([]counted, 0, len(m))
	for k, v := range m {
		out = append(out, counted{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		return out[i].name < out[j].name
	})
	return out
}

// mdCell escapes text for a Markdown table cell.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

// WriteMarkdown writes report.md: summary counts, then the lists a
// reviewer works through, then every recipe.
func (r *Report) WriteMarkdown(w io.Writer) error {
	bw := bufio.NewWriter(w)
	p := func(format string, a ...any) { fmt.Fprintf(bw, format, a...) }

	review, byCat, byReason := 0, map[string]int{}, map[string]int{}
	unknown := map[string]int{}
	var noIng, noSteps []Draft
	for _, d := range r.Drafts {
		rc := d.Recipe
		if rc.NeedsReview {
			review++
		}
		if len(rc.Categories) == 0 {
			byCat["(none)"]++
		}
		for _, c := range rc.Categories {
			byCat[strings.Join(c, " > ")]++
		}
		for _, rs := range rc.ReviewReasons {
			kind, _, _ := strings.Cut(rs, ":")
			byReason[kind]++
			if strings.HasPrefix(rs, reasonUnclassifiedPref) || strings.HasPrefix(rs, reasonUnsplitPref) {
				unknown[rs]++
			}
		}
		if rc.IngredientCount() == 0 {
			noIng = append(noIng, d)
		}
		if rc.StepCount() == 0 {
			noSteps = append(noSteps, d)
		}
	}

	title := r.Collection
	if title == "" {
		title = "(no collection name)"
	}
	p("# Legacy import report: %s\n\n", title)
	p("- Source format: %s\n", r.Format)
	p("- Recipes: %d\n", len(r.Drafts))
	p("- Needing review: %d\n", review)
	p("- Lines ignored before the first title: %d\n", r.PrefixLines)
	if r.HasOlder {
		p("- Older edition format: %s; lines ignored before its first title: %d\n", r.OlderFormat, r.OlderPrefixLines)
	}
	p("\n")

	if len(r.TitleChanges) > 0 {
		p("## Warning: titles changed at existing ordinals\n\n")
		p("These ordinals held a different title in the previous run, so their import keys changed. Importing would create new recipes instead of updating the old ones.\n\n")
		for _, s := range r.TitleChanges {
			p("- %s\n", s)
		}
		p("\n")
	}

	if r.HasOlder {
		var exact int
		var renamed, olderOnly, newerOnly []Draft
		for _, d := range r.Drafts {
			switch r.edition(d) {
			case "both":
				if d.MatchScore >= 1 && !hasReason(d, ReasonTitleChanged) {
					exact++
				} else {
					renamed = append(renamed, d)
				}
			case "older_only":
				olderOnly = append(olderOnly, d)
			case "newer_only":
				newerOnly = append(newerOnly, d)
			}
		}
		p("## Editions\n\n")
		p("- Matched by title: %d\n", exact)
		p("- Matched as likely renames (flagged `%s`): %d\n", ReasonTitleChanged, len(renamed))
		for _, d := range renamed {
			p("  - %s ← %s (similarity %.2f)\n", d.Recipe.Title, d.Older.Recipe.Title, d.MatchScore)
		}
		p("- Only in the older edition (imported, flagged `%s`): %d\n", ReasonOlderOnly, len(olderOnly))
		for _, d := range olderOnly {
			p("  - %s\n", d.Recipe.Title)
		}
		p("- Only in the newer edition: %d\n", len(newerOnly))
		for _, d := range newerOnly {
			p("  - %s\n", d.Recipe.Title)
		}
		p("\n")
	}

	p("## Recipes by category\n\n| Category | Recipes |\n|---|---|\n")
	for _, c := range sortCounts(byCat) {
		p("| %s | %d |\n", mdCell(c.name), c.n)
	}
	p("\n## Review reasons\n\n")
	if len(byReason) == 0 {
		p("None.\n")
	} else {
		p("| Reason | Recipes |\n|---|---|\n")
		for _, c := range sortCounts(byReason) {
			p("| `%s` | %d |\n", c.name, c.n)
		}
	}
	p("\n## Unknown tokens\n\nAdd these to the mapping file, then convert again.\n\n")
	if len(unknown) == 0 {
		p("None.\n")
	} else {
		p("| Kind | Text | Recipes |\n|---|---|---|\n")
		for _, c := range sortCounts(unknown) {
			kind, text, _ := strings.Cut(c.name, ":")
			p("| %s | %s | %d |\n", kind, mdCell(text), c.n)
		}
	}
	list := func(head string, ds []Draft) {
		p("\n## %s\n\n", head)
		if len(ds) == 0 {
			p("None.\n")
		}
		for _, d := range ds {
			p("- %d. %s\n", d.Ordinal, d.Recipe.Title)
		}
	}
	list("Recipes with no ingredients", noIng)
	list("Recipes with no steps", noSteps)

	p("\n## All recipes\n\n| # | Title | Category | Contributor | Flags |\n|---|---|---|---|---|\n")
	for _, d := range r.Drafts {
		rc := d.Recipe
		ord := strconv.Itoa(d.Ordinal)
		if d.OlderOnly {
			ord = "older " + ord
		}
		p("| %s | %s | %s | %s | %s |\n", ord, mdCell(rc.Title), mdCell(CategoryPath(rc)),
			mdCell(rc.ContributedBy), mdCell(strings.Join(rc.ReviewReasons, ", ")))
	}
	return bw.Flush()
}

func hasReason(d Draft, reason string) bool {
	for _, r := range d.Recipe.ReviewReasons {
		if r == reason {
			return true
		}
	}
	return false
}
